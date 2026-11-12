package aiprofile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type migrationState struct {
	Source    string            `json:"source"`
	Committed bool              `json:"committed"`
	Remove    []string          `json:"remove"`
	Digests   map[string]string `json:"digests"`
}

func (m *Manager) initializeStorage() error {
	if e := m.loadRecovery(); e != nil {
		return e
	}
	if e := m.recover(); e != nil {
		m.blocked = true
		return nil
	}
	if next := m.recovery.Migration; next != nil && next.Committed {
		return m.finishMigration()
	}
	source := ""
	if next := m.recovery.Migration; next != nil {
		source = next.Source
	} else {
		if info, e := os.Lstat(filepath.Join(m.dir, "v1")); e == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return StoreInvalid
			}
			source = filepath.Join(m.dir, "v1")
		}
		if source == "" {
			entries, e := os.ReadDir(filepath.Join(m.dir, "profiles"))
			if e != nil {
				return StoreInvalid
			}
			for _, v := range entries {
				if strings.HasSuffix(v.Name(), ".json") {
					source = m.dir
					break
				}
			}
		}
		if source == "" {
			for _, name := range []string{"original", "applied", "pending.json"} {
				if _, e := os.Lstat(filepath.Join(m.dir, name)); e == nil {
					source = m.dir
					break
				}
			}
		}
	}
	if source == "" {
		if m.recovery.Schema == 2 {
			return m.persistRecovery(context.Background(), m.recovery)
		}
		return nil
	}
	if source != m.dir && source != filepath.Join(m.dir, "v1") {
		return StoreInvalid
	}
	var oldLock *os.File
	if source != m.dir {
		f, e := os.OpenFile(filepath.Join(source, ".lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
		if e != nil {
			return StoreInvalid
		}
		if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
			_ = f.Close()
			return Conflict
		}
		oldLock = f
		defer func() { _ = syscall.Flock(int(oldLock.Fd()), syscall.LOCK_UN); _ = oldLock.Close() }()
	}
	legacy := &Manager{usageOnlyDSH: m.usageOnlyDSH, dir: source, owner: m.owner, home: m.home, directories: m.directories, dshVersion: m.dshVersion, legacy: true}
	if e := legacy.recoverLegacy(); e != nil {
		return e
	}
	changes, apps, remove, e := m.migrateContents(legacy)
	if e != nil {
		return e
	}
	next := m.recovery.clone()
	next.Migration = &migrationState{Source: source, Remove: remove, Digests: legacy.legacyObserved}
	if e = m.persistRecovery(context.Background(), next); e != nil {
		return e
	}
	if e = m.stateTransactionApplied(context.Background(), changes, apps); e != nil {
		return e
	}
	next = m.recovery.clone()
	next.Migration.Committed = true
	if e = m.persistRecovery(context.Background(), next); e != nil {
		return e
	}
	return m.finishMigration()
}
func (m *Manager) migrateContents(old *Manager) ([]stateChange, map[CLI]application, []string, error) {
	templates := map[CLI]template{}
	var changes []stateChange
	var remove []string
	for _, cli := range clients {
		var t template
		exists, e := old.readState("templates", string(cli), &t)
		if e != nil {
			return nil, nil, nil, e
		}
		if exists {
			if t.Schema != 1 || t.Revision == 0 {
				return nil, nil, nil, StoreInvalid
			}
			templates[cli] = t
			changes = append(changes, change("templates", string(cli), t))
			if old.dir != m.dir {
				remove = append(remove, filepath.Join("templates", string(cli)+".json"))
			}
		}
	}
	entries, e := os.ReadDir(filepath.Join(old.dir, "profiles"))
	if e != nil {
		return nil, nil, nil, StoreInvalid
	}
	profiles := map[string]savedProfile{}
	for _, entry := range entries {
		if entry.IsDir() {
			if old.dir == m.dir && identifier.MatchString(entry.Name()) {
				continue
			}
			return nil, nil, nil, StoreInvalid
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !identifier.MatchString(id) {
			return nil, nil, nil, StoreInvalid
		}
		var p savedProfile
		if exists, e := old.readState("profiles", id, &p); e != nil || !exists || p.ID != id || p.Schema != 1 || !validProfile(p.Profile, p.Key) {
			return nil, nil, nil, StoreInvalid
		}
		p.Schema = 2
		p.Generated = nil
		c, e := m.renderProfile(&p, templates)
		if e != nil {
			return nil, nil, nil, e
		}
		changes = append(changes, c...)
		changes = append(changes, change("profiles", p.ID, p))
		profiles[id] = p
		remove = append(remove, filepath.Join("profiles", entry.Name()))
	}
	apps := map[CLI]application{}
	for _, cli := range clients {
		for _, kind := range []string{"original", "applied"} {
			var a application
			exists, e := old.readState(kind, string(cli), &a)
			if e != nil {
				return nil, nil, nil, e
			}
			if !exists {
				continue
			}
			if a.Schema != 1 || !m.validNativeSet(cli, a.Files) {
				return nil, nil, nil, StoreInvalid
			}
			remove = append(remove, filepath.Join(kind, string(cli)+".json"))
			id := ""
			if kind == "original" {
				id = matchingLegacyProfile(cli, a.Files, templates[cli], profiles)
			}
			hadProfileID := a.ProfileID != ""
			if kind == "applied" {
				if _, ok := profiles[a.ProfileID]; ok {
					id = a.ProfileID
				}
			}
			if id == "" {
				var p savedProfile
				var c []stateChange
				p, c, e = m.migrationProfile(cli, kind, a.Files, templates)
				if e != nil {
					return nil, nil, nil, e
				}
				id = p.ID
				changes = replaceChanges(changes, c)
				profiles[id] = p
			}
			if kind == "applied" || apps[cli].ProfileID == "" {
				a.Schema = 2
				a.ProfileID = id
				if kind == "original" || !hadProfileID {
					a.ConnectionRevision = profiles[id].ConnectionRevision
					a.TemplateRevision = profiles[id].Generated[cli].TemplateRevision
				}
				if templates[cli].Revision == a.TemplateRevision {
					a.TemplateDigest = digest(encodeState(templates[cli]))
				}
				apps[cli] = a
			}
		}
	}
	if len(profiles) > 256 {
		return nil, nil, nil, Invalid
	}
	return changes, apps, remove, nil
}
func (m *Manager) migrationProfile(cli CLI, kind string, files []nativeFile, templates map[CLI]template) (savedProfile, []stateChange, error) {
	b := encodeState(files)
	h := sha256.Sum256(append([]byte(string(cli)+kind), b...))
	id := hex.EncodeToString(h[:16])
	p := savedProfile{Schema: 2, Profile: Profile{ID: id, Revision: 1, ConnectionRevision: 1, Name: "Imported " + string(cli) + " " + id[:8], Multiplier: 1}}
	_, v, e := transform(cli, files, "", "")
	if e == nil {
		p.BaseURL = v.url
		p.Key = v.key
		p.HasKey = true
	} else {
		p.Kind = "native"
		p.NativeCLI = cli
	}
	var changes []stateChange
	if p.Kind != "native" {
		c, e := m.renderProfile(&p, templates)
		if e != nil {
			return p, nil, e
		}
		changes = append(changes, c...)
	}
	revision := templates[cli].Revision
	if p.Kind == "native" {
		revision = 0
	}
	t := templates[cli]
	t.Revision = revision
	c, e := m.generatedChanges(&p, cli, files, t)
	if e != nil {
		return p, nil, e
	}
	changes = replaceChanges(changes, c)
	changes = append(changes, change("profiles", p.ID, p))
	return p, changes, nil
}
func (m *Manager) finishMigration() error {
	pending := m.recovery.Migration
	if pending == nil {
		return nil
	}
	if !pending.Committed || pending.Source != m.dir && pending.Source != filepath.Join(m.dir, "v1") {
		return RecoveryRequired
	}
	// Load all new metadata and verify every generated byte before deleting a legacy file.
	if e := m.loadProfiles(); e != nil {
		return e
	}
	for _, p := range m.profiles {
		for cli := range p.Generated {
			var t template
			if _, e := m.readState("templates", string(cli), &t); e != nil {
				return e
			}
			if _, e := m.loadGenerated(p, cli, t); e != nil {
				return e
			}
		}
	}
	old := &Manager{usageOnlyDSH: m.usageOnlyDSH, dir: pending.Source, owner: m.owner, legacy: true}
	for _, relative := range pending.Remove {
		if !validSavedPath(relative) {
			return StoreInvalid
		}
		if b, exists, e := old.readSaved(relative); e != nil {
			return e
		} else if exists {
			if expected, ok := pending.Digests[relative]; !ok || digest(b) != expected {
				return RecoveryRequired
			}
			if e = os.Remove(filepath.Join(old.dir, relative)); e != nil {
				return StoreInvalid
			}
		}
		parent := filepath.Dir(filepath.Join(old.dir, relative))
		if _, e := os.Lstat(parent); e == nil {
			if e = syncDirectory(parent); e != nil {
				return e
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return StoreInvalid
		}
	}
	for _, dir := range []string{"original", "applied"} {
		path := filepath.Join(old.dir, dir)
		if e := os.Remove(path); e != nil && !errors.Is(e, os.ErrNotExist) {
			return StoreInvalid
		}
	}
	if old.dir != m.dir {
		for _, dir := range []string{"profiles", "templates"} {
			if e := os.Remove(filepath.Join(old.dir, dir)); e != nil && !errors.Is(e, os.ErrNotExist) {
				return StoreInvalid
			}
		}
		if e := os.Remove(filepath.Join(old.dir, ".lock")); e != nil && !errors.Is(e, os.ErrNotExist) {
			return StoreInvalid
		}
		if e := os.Remove(old.dir); e != nil && !errors.Is(e, os.ErrNotExist) {
			return StoreInvalid
		}
	}
	if e := syncDirectory(m.dir); e != nil {
		return e
	}
	next := m.recovery.clone()
	next.Migration = nil
	return m.persistRecovery(context.Background(), next)
}

// A first-import backup identical in semantics to an existing API Profile does not
// need a duplicate list entry. Different native settings remain separate Profiles.
func matchingLegacyProfile(cli CLI, files []nativeFile, t template, profiles map[string]savedProfile) string {
	_, v, e := transform(cli, files, "", "")
	if e != nil {
		return ""
	}
	normalized, _, e := transform(cli, files, v.url, v.key)
	if e != nil {
		return ""
	}
	best := ""
	for id, p := range profiles {
		if p.Kind != "" || p.BaseURL != v.url || p.Key != v.key {
			continue
		}
		candidate, _, e := transform(cli, t.Files, p.BaseURL, p.Key)
		if e == nil && sameFiles(candidate, normalized) && (best == "" || id < best) {
			best = id
		}
	}
	return best
}
