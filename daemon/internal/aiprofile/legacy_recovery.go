package aiprofile

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Finish a v1 transaction before changing its storage format. Never remove evidence
// when an external writer has introduced bytes outside the old/new transaction set.
func (m *Manager) recoverLegacy() error {
	f := m.privateFile("pending.json", 24<<20)
	b, exists, e := f.Read()
	if e != nil {
		return RecoveryRequired
	}
	if !exists {
		return nil
	}
	var p pending
	if json.Unmarshal(b, &p) != nil || p.Schema != 1 {
		return RecoveryRequired
	}
	if m.usageOnlyDSH && p.CLI == DSH {
		return RecoveryRequired
	}
	ctx := context.Background()
	if len(p.Changes) > 0 {
		for _, c := range p.Changes {
			if !validStatePath(c.Kind, c.ID) {
				return RecoveryRequired
			}
			relative := filepath.Join(c.Kind, c.ID+".json")
			data, ok, e := m.readSaved(relative)
			if e != nil {
				return RecoveryRequired
			}
			if ok && string(data) != string(c.Before) && string(data) != string(c.After) {
				return RecoveryRequired
			}
			if c.Existed {
				if e = m.writeSaved(ctx, relative, c.Before); e != nil {
					return e
				}
			} else if e = os.Remove(filepath.Join(m.dir, relative)); e != nil && !errors.Is(e, os.ErrNotExist) {
				return RecoveryRequired
			}
		}
	} else {
		if p.Directory != m.targetDir(p.CLI) || !m.validNativeSet(p.CLI, p.Before) || !m.validNativeSet(p.CLI, p.After) {
			return RecoveryRequired
		}
		files, e := m.native(p.CLI)
		if e != nil {
			return RecoveryRequired
		}
		if sameFiles(files, p.After) {
			if e = m.writeState(ctx, "applied", string(p.CLI), p.Application); e != nil {
				return e
			}
		} else {
			for i, f := range files {
				if !sameFiles([]nativeFile{f}, []nativeFile{p.Before[i]}) && !sameFiles([]nativeFile{f}, []nativeFile{p.After[i]}) {
					return RecoveryRequired
				}
			}
			for i, f := range p.Before {
				if e = m.replaceNative(p.CLI, f, files[i].Exists); e != nil {
					return RecoveryRequired
				}
			}
		}
	}
	if e = os.Remove(filepath.Join(m.dir, "pending.json")); e != nil {
		return RecoveryRequired
	}
	return syncDirectory(m.dir)
}
