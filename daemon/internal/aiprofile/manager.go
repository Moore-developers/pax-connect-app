package aiprofile

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Options fixes trusted locations at daemon composition, not from RPC input.
type Options struct {
	stateWriteHook  func(int) error
	UsageOnlyDSH    bool
	ModelClient     *http.Client
	DSHVersion      string
	Directory, Home string
	Owner           uint32
	Directories     map[CLI]string
}

// Manager is the sole serial owner of saved Profiles and CLI application state.
type Manager struct {
	usageOnlyDSH   bool
	events         *sql.DB
	mu             sync.Mutex
	modelClient    *http.Client
	recovery       recoveryState
	legacy         bool
	legacyObserved map[string]string
	dir, home      string
	owner          uint32
	lock           *os.File
	profiles       map[string]savedProfile
	directories    map[CLI]string
	observations   map[CLI]observation
	plans          map[string]planned
	blocked        bool
	renameHook     func(int) error
	stateWriteHook func(int) error
	dshVersion     string
}

// New prepares private Pax state without creating or changing CLI configuration.
func New(o Options) (*Manager, error) {
	if o.Owner == 0 || o.Owner != uint32(os.Geteuid()) || !filepath.IsAbs(o.Directory) || !filepath.IsAbs(o.Home) {
		return nil, Unsafe
	}
	m := &Manager{usageOnlyDSH: o.UsageOnlyDSH, dir: o.Directory, home: o.Home, owner: o.Owner, directories: o.Directories, observations: map[CLI]observation{}, plans: map[string]planned{}, dshVersion: o.DSHVersion, stateWriteHook: o.stateWriteHook, modelClient: o.ModelClient}
	if err := m.prepare(); err != nil {
		return nil, err
	}
	if err := m.initializeStorage(); err != nil {
		_ = m.Close()
		return nil, err
	}
	if err := m.recover(); err != nil {
		m.blocked = true
	}
	if !m.blocked {
		if e := m.removeEmptyProfiles(); e != nil {
			_ = m.Close()
			return nil, e
		}
	}
	if err := m.loadProfiles(); err != nil {
		_ = m.Close()
		return nil, err
	}
	if err := m.openEvents(); err != nil {
		_ = m.Close()
		return nil, err
	}
	if err := m.repairEvents(context.Background()); err != nil {
		_ = m.Close()
		return nil, err
	}
	return m, nil
}

// Close releases the instance lock. It never removes saved data.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.events != nil {
		if e := m.events.Close(); e != nil {
			return StoreInvalid
		}
		m.events = nil
	}
	if m.lock == nil {
		return nil
	}
	err := syscall.Flock(int(m.lock.Fd()), syscall.LOCK_UN)
	closeErr := m.lock.Close()
	m.lock = nil
	if err != nil {
		return cause(err)
	}
	return cause(closeErr)
}

// Query returns the current secret-free persisted state.
func (m *Manager) Query(ctx context.Context) (Workspace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.workspace(ctx)
}

// Pricing returns the multipliers a Profile charges right now: its default and the
// per-model overrides. found is false for an unknown Profile.
func (m *Manager) Pricing(ctx context.Context, id string) (multiplier float64, models map[string]float64, found bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() != nil {
		return 0, nil, false, ctx.Err()
	}
	p, ok := m.profiles[id]
	if !ok {
		return 0, nil, false, nil
	}
	models = make(map[string]float64, len(p.ModelMultipliers))
	for k, v := range p.ModelMultipliers {
		models[k] = v
	}
	return p.Multiplier, models, true, nil
}
func (m *Manager) workspace(ctx context.Context) (Workspace, error) {
	if ctx.Err() != nil {
		return Workspace{}, ctx.Err()
	}
	if e := m.repairEvents(ctx); e != nil {
		return Workspace{}, e
	}
	w := Workspace{Profiles: []Profile{}, Bindings: []Binding{}}
	for _, p := range m.profiles {
		w.Profiles = append(w.Profiles, p.Profile)
	}
	sort.Slice(w.Profiles, func(i, j int) bool { return w.Profiles[i].Name < w.Profiles[j].Name })
	for _, cli := range clients {
		if cli == DSH && m.usageOnlyDSH {
			w.Bindings = append(w.Bindings, Binding{CLI: DSH, State: "read_only", Reason: string(Unsupported)})
			continue
		}
		b, e := m.inspect(ctx, cli)
		if e != nil {
			return Workspace{}, e
		}
		if m.blocked {
			b.State = "recovery_required"
			b.Reason = string(RecoveryRequired)
		}
		w.Bindings = append(w.Bindings, b)
	}
	return w, nil
}

// Mutate saves Pax metadata and credentials separately from applying native files.
func (m *Manager) Mutate(ctx context.Context, r Mutation) (Workspace, error) {
	w, e := m.mutate(ctx, r)
	if e != nil || m.modelClient == nil || (r.Action != "save" && r.Action != "import") {
		return w, e
	}
	id := ""
	m.mu.Lock()
	for _, p := range m.profiles {
		if p.OperationID == r.OperationID && p.Kind != "native" && p.ModelsState == "" {
			id = p.ID
			break
		}
	}
	m.mu.Unlock()
	if id != "" {
		return m.RefreshModels(ctx, id)
	}
	return w, nil
}
func (m *Manager) mutate(ctx context.Context, r Mutation) (Workspace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() != nil {
		return Workspace{}, ctx.Err()
	}
	if m.usageOnlyDSH && r.CLI == DSH {
		return Workspace{}, Unsupported
	}
	if r.OperationID == "" || len(r.OperationID) > 128 {
		return Workspace{}, Invalid
	}
	if m.blocked {
		return Workspace{}, RecoveryRequired
	}
	switch r.Action {
	case "import":
		if e := m.importCurrent(ctx, r); e != nil {
			return Workspace{}, e
		}
		return m.workspace(ctx)
	case "delete":
		if e := m.deleteProfile(ctx, r); e != nil {
			return Workspace{}, e
		}
		return m.workspace(ctx)
	case "initialize_template":
		return Workspace{}, Unsupported
	case "save":
	default:
		return Workspace{}, Invalid
	}
	old, exists := m.profiles[r.Profile.ID]
	key := r.Key
	if key == "" {
		key = old.Key
	}
	p := r.Profile
	p.Name = strings.TrimSpace(p.Name)
	if len(p.ModelMultipliers) == 0 {
		p.ModelMultipliers = nil
	}
	if !validInputMultipliers(p) {
		return Workspace{}, Invalid
	}
	if old.Kind == "native" {
		p.Kind = old.Kind
		p.NativeCLI = old.NativeCLI
	}
	if old.Kind != "native" && !validProfile(p, key) || old.Kind == "native" && (!validMetadata(p) || p.BaseURL != "" || r.Key != "") {
		return Workspace{}, Invalid
	}
	if exists && old.OperationID == r.OperationID {
		if !sameProfileInput(old.Profile, p) || old.Key != key {
			return Workspace{}, Conflict
		}
		return m.workspace(ctx)
	}
	if exists && old.Revision != r.ExpectedRevision || !exists && r.ExpectedRevision != 0 {
		return Workspace{}, Conflict
	}
	if !exists && len(m.profiles) >= 256 {
		return Workspace{}, Invalid
	}
	for id, v := range m.profiles {
		if id != p.ID && v.Name == p.Name {
			return Workspace{}, Invalid
		}
	}
	if old.ModelsState != "" {
		p.ModelIDs = old.ModelIDs
	}
	p.Catalog = old.Catalog
	p.ModelsState = old.ModelsState
	p.ModelsEndpoint = old.ModelsEndpoint
	p.ModelsChecked = old.ModelsChecked
	p.ModelsError = old.ModelsError
	p.Revision = old.Revision + 1
	p.ConnectionRevision = old.ConnectionRevision
	if !exists || old.Key != key || old.BaseURL != p.BaseURL {
		p.ConnectionRevision++
		p.Catalog = nil
		if exists {
			p.ModelIDs = nil
		}
		p.ModelsState = ""
		p.ModelsEndpoint = ""
		p.ModelsChecked = 0
		p.ModelsError = ""
	}
	p.HasKey = key != ""
	value := savedProfile{Schema: 2, Profile: p, Key: key, OperationID: r.OperationID, Generated: cloneGenerated(old.Generated), MetadataAt: time.Now().UnixNano()}
	if err := m.saveProfile(ctx, value); err != nil {
		return Workspace{}, err
	}
	if e := m.repairEvents(ctx); e != nil {
		return Workspace{}, e
	}
	return m.workspace(ctx)
}
