package aiprofile

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"pax-connect/daemon/internal/storage"
)

type stateChange struct {
	Kind, ID, Path       string
	Before, After        []byte
	Existed, AfterExists bool
}
type pending struct {
	Schema        int
	CLI           CLI
	Directory     string
	Before, After []nativeFile
	Application   application
	Changes       []stateChange
}
type recoveryState struct {
	// UsageRatesVersion records the completed 2026-10-28 rate adoption; kept for decoding only.
	UsageRatesVersion int                 `json:"usage_rates_version,omitempty"`
	Schema            int                 `json:"schema"`
	Applied           map[CLI]application `json:"applied"`
	Pending           *pending            `json:"pending,omitempty"`
	Migration         *migrationState     `json:"migration,omitempty"`
}

func (r recoveryState) clone() recoveryState {
	next := r
	next.Applied = map[CLI]application{}
	for c, a := range r.Applied {
		next.Applied[c] = a
	}
	return next
}
func change(kind, id string, value any) stateChange {
	return stateChange{Kind: kind, ID: id, After: encodeState(value), AfterExists: true}
}
func (m *Manager) recoveryFile() *storage.PrivateFile { return m.privateFile("recovery.json", 64<<20) }
func (m *Manager) persistRecovery(ctx context.Context, next recoveryState) error {
	next.Schema = 2
	e := m.recoveryFile().Write(ctx, encodeState(next))
	if e != nil {
		m.blocked = true
		if errors.Is(e, storage.ErrCommitUncertain) {
			return Uncertain
		}
		return StoreInvalid
	}
	m.recovery = next
	return nil
}
func (m *Manager) loadRecovery() error {
	b, exists, e := m.recoveryFile().Read()
	if e != nil {
		return StoreInvalid
	}
	if !exists {
		m.recovery = recoveryState{Schema: 2, Applied: map[CLI]application{}}
		return nil
	}
	if json.Unmarshal(b, &m.recovery) != nil || m.recovery.Schema != 2 {
		return StoreInvalid
	}
	if m.recovery.Applied == nil {
		m.recovery.Applied = map[CLI]application{}
	}
	return nil
}
func (m *Manager) saveJournal(ctx context.Context, p pending) error {
	next := m.recovery.clone()
	next.Pending = &p
	return m.persistRecovery(ctx, next)
}
func (m *Manager) clearJournal() error {
	next := m.recovery.clone()
	next.Pending = nil
	return m.persistRecovery(context.Background(), next)
}
func (m *Manager) stateTransaction(ctx context.Context, changes []stateChange) error {
	return m.stateTransactionApplied(ctx, changes, nil)
}
func (m *Manager) stateTransactionApplied(ctx context.Context, changes []stateChange, applications map[CLI]application) error {
	for i, c := range changes {
		if c.Path == "" {
			path, e := m.statePath(c.Kind, c.ID)
			if e != nil {
				return e
			}
			c.Path = path
		}
		if !validSavedPath(c.Path) {
			return StoreInvalid
		}
		b, ok, e := m.readSaved(c.Path)
		if e != nil {
			return e
		}
		c.Before = b
		c.Existed = ok
		changes[i] = c
	}
	if e := m.saveJournal(ctx, pending{Schema: 2, Changes: changes}); e != nil {
		return e
	}
	for i, c := range changes {
		var e error
		if m.stateWriteHook != nil {
			e = m.stateWriteHook(i)
		}
		if e == nil {
			e = m.applyChange(ctx, c, false)
		}
		if e != nil {
			if m.recover() != nil {
				m.blocked = true
				return RecoveryRequired
			}
			return e
		}
	}
	next := m.recovery.clone()
	for cli, a := range applications {
		next.Applied[cli] = a
	}
	next.Pending = nil
	if e := m.persistRecovery(ctx, next); e != nil {
		return e
	}
	return nil
}
func (m *Manager) applyChange(ctx context.Context, c stateChange, before bool) error {
	data, exists := c.After, c.AfterExists
	if before {
		data, exists = c.Before, c.Existed
	}
	if exists {
		return m.writeSaved(ctx, c.Path, data)
	}
	path := filepath.Join(m.dir, c.Path)
	parent := filepath.Dir(path)
	if _, e := os.Lstat(parent); errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e := m.safeParent(parent); e != nil {
		return e
	}
	if e := os.Remove(path); e != nil && !errors.Is(e, os.ErrNotExist) {
		return StoreInvalid
	}
	if _, e := os.Stat(parent); errors.Is(e, os.ErrNotExist) {
		return nil
	}
	return syncDirectory(parent)
}
func validStatePath(kind, id string) bool {
	if kind == "profiles" {
		return identifier.MatchString(id)
	}
	if kind != "original" && kind != "templates" && kind != "applied" {
		return false
	}
	for _, c := range clients {
		if id == string(c) {
			return true
		}
	}
	return false
}
func (m *Manager) recover() error {
	if m.recovery.Pending == nil {
		return nil
	}
	p := *m.recovery.Pending
	if m.usageOnlyDSH && p.CLI == DSH {
		return RecoveryRequired
	}
	if p.Schema != 2 {
		return RecoveryRequired
	}
	ctx := context.Background()
	if len(p.Changes) > 0 {
		for _, c := range p.Changes {
			if !validSavedPath(c.Path) {
				return RecoveryRequired
			}
			b, ok, e := m.readSaved(c.Path)
			if e != nil {
				return RecoveryRequired
			}
			matchesBefore := c.Existed && string(b) == string(c.Before)
			matchesAfter := c.AfterExists && string(b) == string(c.After)
			if ok && !matchesBefore && !matchesAfter {
				return RecoveryRequired
			}
		}
		for i := len(p.Changes) - 1; i >= 0; i-- {
			if e := m.applyChange(ctx, p.Changes[i], true); e != nil {
				return RecoveryRequired
			}
		}
		if e := m.removeEmptyProfiles(); e != nil {
			return e
		}
		return m.clearJournal()
	}
	if p.Directory != m.targetDir(p.CLI) || !m.validNativeSet(p.CLI, p.Before) || !m.validNativeSet(p.CLI, p.After) {
		return RecoveryRequired
	}
	current, e := m.native(p.CLI)
	if e != nil {
		return RecoveryRequired
	}
	if sameFiles(current, p.After) {
		next := m.recovery.clone()
		next.Applied[p.CLI] = p.Application
		next.Pending = nil
		return m.persistRecovery(ctx, next)
	}
	for i, f := range current {
		if !sameFiles([]nativeFile{f}, []nativeFile{p.Before[i]}) && !sameFiles([]nativeFile{f}, []nativeFile{p.After[i]}) {
			return RecoveryRequired
		}
	}
	for i, f := range p.Before {
		observed, err := m.native(p.CLI)
		if err != nil || !sameObservation(observed, current) {
			return RecoveryRequired
		}
		if sameFiles([]nativeFile{observed[i]}, []nativeFile{f}) {
			continue
		}
		if e = m.replaceNative(p.CLI, f, observed[i].Exists); e != nil {
			return RecoveryRequired
		}
		current[i] = f
		verified, err := m.native(p.CLI)
		if err != nil || !sameFiles(verified, current) {
			return RecoveryRequired
		}
		current = verified
	}
	return m.clearJournal()
}
func (m *Manager) removeEmptyProfiles() error {
	entries, e := os.ReadDir(filepath.Join(m.dir, "profiles"))
	if e != nil {
		return StoreInvalid
	}
	for _, v := range entries {
		if !v.IsDir() || !identifier.MatchString(v.Name()) {
			continue
		}
		root := filepath.Join(m.dir, "profiles", v.Name())
		if _, e = os.Lstat(filepath.Join(root, "profile.json")); errors.Is(e, os.ErrNotExist) {
			if e = removeEmptyTree(root); e != nil {
				return StoreInvalid
			}
		}
	}
	return nil
}
func removeEmptyTree(root string) error {
	entries, e := os.ReadDir(root)
	if e != nil {
		return e
	}
	for _, v := range entries {
		if !v.IsDir() {
			return StoreInvalid
		}
		if e = removeEmptyTree(filepath.Join(root, v.Name())); e != nil {
			return e
		}
	}
	if e := os.Remove(root); e != nil {
		return e
	}
	return syncDirectory(filepath.Dir(root))
}
