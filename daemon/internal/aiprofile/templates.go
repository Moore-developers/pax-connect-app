package aiprofile

import (
	"context"
	"path/filepath"
	"time"
)

type observation struct {
	id    string
	files []nativeFile
	at    time.Time
}
type application struct {
	ConnectionDigest   string             `json:"connection_sha256,omitempty"`
	EventSource        string             `json:"event_source,omitempty"`
	EventAt            int64              `json:"event_at,omitempty"`
	EventName          string             `json:"event_name,omitempty"`
	EventMultiplier    float64            `json:"event_multiplier,omitempty"`
	EventModels        map[string]float64 `json:"event_models,omitempty"`
	PlanID             string             `json:"plan_id"`
	Schema             int                `json:"schema"`
	ProfileID          string             `json:"profile_id"`
	ConnectionRevision uint64             `json:"connection_revision"`
	TemplateRevision   uint64             `json:"template_revision"`
	TemplateDigest     string             `json:"template_sha256,omitempty"`
	OperationID        string             `json:"operation_id"`
	Files              []nativeFile       `json:"files"`
}
type planned struct {
	Plan
	request            PlanRequest
	files, before      []nativeFile
	connectionRevision uint64
}

func (m *Manager) inspect(ctx context.Context, cli CLI) (Binding, error) {
	b := Binding{CLI: cli, ServiceSource: "unknown", State: "not_imported", TargetPath: m.targetDir(cli)}
	if cli == DSH && m.usageOnlyDSH {
		b.State = "read_only"
		b.Reason = string(Unsupported)
		return b, nil
	}
	if cli == DSH && !m.supportsDSH() {
		b.Reason = string(Unsupported)
		return b, nil
	}
	files, e := m.native(cli)
	if e != nil {
		b.State = "invalid"
		b.Reason = e.Error()
		return b, nil
	}
	ob := m.observations[cli]
	if ob.id == "" || !sameObservation(ob.files, files) || time.Since(ob.at) > time.Minute {
		ob = observation{id: newID(), files: files, at: time.Now()}
		m.observations[cli] = ob
	}
	b.InspectionID = ob.id
	_, v, validation := transform(cli, files, "", "")
	if validation == nil {
		_, validation = patchCurrent(cli, files, v.url, v.key)
	}
	b.State = "ready"
	if validation != nil {
		b.State, _ = m.initializationState(cli, files)
		if b.State == "missing" && cli == Codex {
			b.RequiresModel = codexNeedsModel(files)
		}
		if b.State == "invalid" {
			b.Reason = string(Unsupported)
		}
	} else if validEndpoint(v.url) {
		b.Endpoint = v.url
	} else {
		b.State = "invalid"
		b.Reason = string(Invalid)
		validation = Invalid
	}
	if b.State == "account" {
		b.ServiceSource = "official_account"
	}
	if validation == nil {
		b.ServiceSource = sourceOfEndpoint(cli, v.url)
	}
	a, applied := m.recovery.Applied[cli]
	if !applied {
		return b, nil
	}
	p, ok := m.profiles[a.ProfileID]
	if !ok {
		return b, StoreInvalid
	}
	b.ProfileID = a.ProfileID
	if p.Kind == "native" && sameFiles(files, a.Files) {
		b.State = "applied"
		b.ServiceSource = "official_account"
		b.Reason = ""
		return b, nil
	}
	if validation != nil {
		b.ProfileID = ""
		return b, nil
	}
	switch {
	case matchesConnection(cli, files, p):
		b.State = "applied"
	case a.ConnectionRevision != p.ConnectionRevision && a.ConnectionDigest != "" && connectionDigest(cli, files) == a.ConnectionDigest:
		b.State = "pending"
	default:
		b.State = "changed"
	}
	if b.State == "changed" {
		b.ProfileID = ""
	}
	return b, nil
}
func (m *Manager) checkedObservation(cli CLI, id string) ([]nativeFile, error) {
	ob, ok := m.observations[cli]
	if !ok || id == "" || ob.id != id || time.Since(ob.at) > time.Minute {
		return nil, Changed
	}
	files, e := m.native(cli)
	if e != nil {
		return nil, e
	}
	if !sameObservation(files, ob.files) {
		return nil, Changed
	}
	return files, nil
}
func (m *Manager) importCurrent(ctx context.Context, r Mutation) error {
	if r.CLI == DSH && !m.supportsDSH() {
		return Unsupported
	}
	for _, p := range m.profiles {
		if p.OperationID == r.OperationID {
			return nil
		}
	}
	files, e := m.checkedObservation(r.CLI, r.InspectionID)
	if e != nil {
		return e
	}
	_, v, e := transform(r.CLI, files, "", "")
	if e != nil {
		return e
	}
	p := savedProfile{Schema: 2, Profile: r.Profile, Key: v.key, OperationID: r.OperationID}
	if p.ID == "" {
		p.ID = newID()
	}
	p.BaseURL = v.url
	p.Revision = 1
	p.ConnectionRevision = 1
	p.HasKey = true
	if !validProfile(p.Profile, p.Key) || !validInputMultipliers(p.Profile) || len(m.profiles) >= 256 {
		return Invalid
	}
	for _, old := range m.profiles {
		if old.Name == p.Name {
			return Invalid
		}
	}
	changes := []stateChange{change("profiles", p.ID, p)}
	apps := map[CLI]application{}
	if _, ok := m.recovery.Applied[r.CLI]; !ok {
		apps[r.CLI] = application{Schema: 2, ProfileID: p.ID, ConnectionRevision: p.ConnectionRevision, ConnectionDigest: connectionDigest(r.CLI, files)}
	}
	if e = m.stateTransactionApplied(ctx, changes, apps); e != nil {
		return e
	}
	return m.loadProfiles()
}
func replaceChanges(existing, next []stateChange) []stateChange {
	for _, c := range next {
		found := false
		for i, old := range existing {
			if c.Path != "" && old.Path == c.Path {
				existing[i] = c
				found = true
				break
			}
		}
		if !found {
			existing = append(existing, c)
		}
	}
	return existing
}
func (m *Manager) deleteProfile(ctx context.Context, r Mutation) error {
	p, ok := m.profiles[r.Profile.ID]
	if !ok {
		return nil
	}
	if p.Revision != r.ExpectedRevision {
		return Conflict
	}
	next := m.recovery.clone()
	for cli, a := range m.recovery.Applied {
		if a.ProfileID != p.ID {
			continue
		}
		b, err := m.inspect(ctx, cli)
		if err != nil {
			return err
		}
		if b.ProfileID == p.ID {
			return InUse
		}
		delete(next.Applied, cli)
	}
	if len(next.Applied) != len(m.recovery.Applied) {
		if err := m.repairEvents(ctx); err != nil {
			return err
		}
		if err := m.persistRecovery(ctx, next); err != nil {
			return err
		}
	}
	var changes []stateChange
	for cli, set := range p.Generated {
		for _, f := range set.Files {
			changes = append(changes, stateChange{Path: filepath.Join("profiles", p.ID, string(cli), f.Name)})
		}
	}
	changes = append(changes, stateChange{Path: filepath.Join("profiles", p.ID, "profile.json")})
	if e := m.stateTransaction(ctx, changes); e != nil {
		return e
	}
	delete(m.profiles, p.ID)
	if e := removeEmptyTree(filepath.Join(m.dir, "profiles", p.ID)); e != nil {
		return StoreInvalid
	}
	return syncDirectory(filepath.Join(m.dir, "profiles"))
}

// Application status concerns the selected provider and credential. Unrelated CLI
// preferences (including MCP entries and whitespace) may change independently.
func matchesConnection(cli CLI, files []nativeFile, p savedProfile) bool {
	if p.Kind == "native" {
		return false
	}
	_, v, e := transform(cli, files, "", "")
	return e == nil && v.url == p.BaseURL && v.key == p.Key
}
