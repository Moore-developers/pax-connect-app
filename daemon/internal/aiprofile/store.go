package aiprofile

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"pax-connect/daemon/internal/storage"
	"strings"
	"syscall"
)

func (m *Manager) statePath(kind, id string) (string, error) {
	if !validStatePath(kind, id) {
		return "", StoreInvalid
	}
	if kind == "profiles" && !m.legacy {
		return filepath.Join("profiles", id, "profile.json"), nil
	}
	return filepath.Join(kind, id+".json"), nil
}
func (m *Manager) readState(kind, id string, out any) (bool, error) {
	if kind == "applied" && !m.legacy {
		a, ok := m.recovery.Applied[CLI(id)]
		if !ok {
			return false, nil
		}
		b, _ := json.Marshal(a)
		return true, cause(json.Unmarshal(b, out))
	}
	path, e := m.statePath(kind, id)
	if e != nil {
		return false, e
	}
	b, exists, e := m.readSaved(path)
	if e != nil || !exists {
		return exists, e
	}
	if json.Unmarshal(b, out) != nil {
		return false, StoreInvalid
	}
	return true, nil
}
func (m *Manager) writeState(ctx context.Context, kind, id string, value any) error {
	if kind == "applied" && !m.legacy {
		a, ok := value.(application)
		if !ok {
			return StoreInvalid
		}
		next := m.recovery.clone()
		next.Applied[CLI(id)] = a
		return m.persistRecovery(ctx, next)
	}
	path, e := m.statePath(kind, id)
	if e != nil {
		return e
	}
	b, e := json.Marshal(value)
	if e != nil {
		return StoreInvalid
	}
	return m.writeSaved(ctx, path, b)
}
func (m *Manager) privateFile(path string, limit int64) *storage.PrivateFile {
	return storage.NewPrivateFile(filepath.Join(m.dir, path), m.owner, limit)
}
func (m *Manager) readSaved(path string) ([]byte, bool, error) {
	if !validSavedPath(path) {
		return nil, false, StoreInvalid
	}
	parent := filepath.Dir(filepath.Join(m.dir, path))
	if _, e := os.Lstat(parent); errors.Is(e, os.ErrNotExist) {
		return nil, false, nil
	}
	b, ok, e := m.privateFile(path, 8<<20).Read()
	if m.legacy && e == nil && ok {
		if m.legacyObserved == nil {
			m.legacyObserved = map[string]string{}
		}
		m.legacyObserved[path] = digest(b)
	}
	return b, ok, cause(e)
}
func (m *Manager) writeSaved(ctx context.Context, path string, b []byte) error {
	if !validSavedPath(path) {
		return StoreInvalid
	}
	if e := m.prepareSavedParent(path); e != nil {
		return e
	}
	e := m.privateFile(path, 8<<20).Write(ctx, b)
	if errors.Is(e, storage.ErrCommitUncertain) {
		m.blocked = true
		return Uncertain
	}
	return cause(e)
}
func (m *Manager) prepareSavedParent(path string) error {
	relative := filepath.Dir(path)
	current := m.dir
	if relative == "." {
		return nil
	}
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		if e := storage.PreparePrivateDirectory(current, m.owner); e != nil {
			return StoreInvalid
		}
		if e := syncDirectory(filepath.Dir(current)); e != nil {
			return e
		}
	}
	return nil
}
func validSavedPath(path string) bool {
	if filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	parts := strings.Split(path, string(filepath.Separator))
	if len(parts) == 2 && (parts[0] == "templates" || parts[0] == "original" || parts[0] == "applied") {
		return validStatePath(parts[0], strings.TrimSuffix(parts[1], ".json")) && strings.HasSuffix(parts[1], ".json")
	}
	if len(parts) == 2 && parts[0] == "profiles" {
		return strings.HasSuffix(parts[1], ".json") && identifier.MatchString(strings.TrimSuffix(parts[1], ".json"))
	}
	if len(parts) < 3 || parts[0] != "profiles" || !identifier.MatchString(parts[1]) {
		return false
	}
	if len(parts) == 3 {
		return parts[2] == "profile.json"
	}
	if parts[2] != "codex" && parts[2] != "claude" && parts[2] != "dsh" {
		return false
	}
	name := filepath.Join(parts[3:]...)
	switch CLI(parts[2]) {
	case Codex:
		return name == "config.toml" || name == "auth.json"
	case Claude:
		return name == "settings.json"
	case DSH:
		return name == "settings.yaml" || name == ".credentials.yaml" || name == "profiles/desktop/cordis.patch.yml"
	}
	return false
}
func (m *Manager) loadProfiles() error {
	entries, e := os.ReadDir(filepath.Join(m.dir, "profiles"))
	if e != nil {
		return StoreInvalid
	}
	profiles := map[string]savedProfile{}
	for _, entry := range entries {
		if entry.Name() == ".DS_Store" {
			continue
		}
		if !entry.IsDir() && m.recovery.Migration != nil && m.recovery.Migration.Committed && m.recovery.Migration.Source == m.dir {
			known := false
			for _, name := range m.recovery.Migration.Remove {
				if name == filepath.Join("profiles", entry.Name()) {
					known = true
				}
			}
			if known {
				continue
			}
		}
		if !entry.IsDir() || !identifier.MatchString(entry.Name()) {
			return StoreInvalid
		}
		var p savedProfile
		if exists, e := m.readState("profiles", entry.Name(), &p); e != nil || !exists {
			return StoreInvalid
		}
		if p.Schema != 2 || p.ID != entry.Name() || p.Revision == 0 || !validSavedProfile(p) {
			return StoreInvalid
		}
		p.HasKey = p.Key != ""
		profiles[p.ID] = p
	}
	if len(profiles) > 256 {
		return StoreInvalid
	}
	m.profiles = profiles
	return nil
}
func (m *Manager) prepare() error {
	for _, p := range []string{m.dir, filepath.Join(m.dir, "profiles"), filepath.Join(m.dir, "templates")} {
		if e := storage.PreparePrivateDirectory(p, m.owner); e != nil {
			return StoreInvalid
		}
		if e := syncDirectory(filepath.Dir(p)); e != nil {
			return e
		}
	}
	f, e := os.OpenFile(filepath.Join(m.dir, ".lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return StoreInvalid
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		_ = f.Close()
		return Conflict
	}
	m.lock = f
	return nil
}
