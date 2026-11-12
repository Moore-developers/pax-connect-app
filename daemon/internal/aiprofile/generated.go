package aiprofile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
)

type generatedFile struct {
	Name   string `json:"name"`
	Exists bool   `json:"exists"`
	Digest string `json:"sha256"`
}
type generatedSet struct {
	ConnectionRevision uint64          `json:"connection_revision"`
	TemplateRevision   uint64          `json:"template_revision"`
	TemplateDigest     string          `json:"template_sha256"`
	Files              []generatedFile `json:"files"`
}

func digest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func validSavedProfile(p savedProfile) bool {
	if p.Kind == "native" {
		return identifier.MatchString(p.ID) && strings.TrimSpace(p.Name) != "" && len(p.Name) <= 256 && (p.NativeCLI == Codex || p.NativeCLI == Claude || p.NativeCLI == DSH) && validMetadata(p.Profile) && p.Key == "" && p.BaseURL == "" && len(p.Generated) == 1
	}
	return p.Kind == "" && validProfile(p.Profile, p.Key)
}
func (m *Manager) generatedChanges(p *savedProfile, cli CLI, files []nativeFile, t template) ([]stateChange, error) {
	if !m.validNativeSet(cli, files) {
		return nil, StoreInvalid
	}
	if p.Generated == nil {
		p.Generated = map[CLI]generatedSet{}
	}
	set := generatedSet{ConnectionRevision: p.ConnectionRevision, TemplateRevision: t.Revision, TemplateDigest: digest(encodeState(t))}
	var changes []stateChange
	for _, f := range files {
		path := filepath.Join("profiles", p.ID, string(cli), f.Name)
		set.Files = append(set.Files, generatedFile{Name: f.Name, Exists: f.Exists, Digest: digest(f.Data)})
		if f.Exists {
			changes = append(changes, stateChange{Path: path, After: f.Data, AfterExists: true})
		} else {
			changes = append(changes, stateChange{Path: path})
		}
	}
	p.Generated[cli] = set
	return changes, nil
}
func (m *Manager) renderProfile(p *savedProfile, overrides map[CLI]template) ([]stateChange, error) {
	var changes []stateChange
	for _, cli := range clients {
		t, ok := overrides[cli]
		if !ok {
			exists, e := m.readState("templates", string(cli), &t)
			if e != nil {
				return nil, e
			}
			if !exists {
				continue
			}
		}
		if cli == DSH && !m.supportsDSH() {
			continue
		}
		files, _, e := transform(cli, t.Files, p.BaseURL, p.Key)
		if e != nil {
			return nil, e
		}
		c, e := m.generatedChanges(p, cli, files, t)
		if e != nil {
			return nil, e
		}
		changes = append(changes, c...)
	}
	return changes, nil
}
func (m *Manager) loadGenerated(p savedProfile, cli CLI, t template) ([]nativeFile, error) {
	set, ok := p.Generated[cli]
	if !ok || set.ConnectionRevision != p.ConnectionRevision {
		return nil, Unsupported
	}
	if p.Kind == "native" {
		if p.NativeCLI != cli {
			return nil, Unsupported
		}
	} else if set.TemplateRevision != t.Revision || set.TemplateDigest != digest(encodeState(t)) {
		return nil, Conflict
	}
	if len(set.Files) != len(m.targetNames(cli)) {
		return nil, StoreInvalid
	}
	var files []nativeFile
	for _, g := range set.Files {
		path := filepath.Join("profiles", p.ID, string(cli), g.Name)
		b, exists, e := m.readSaved(path)
		if e != nil || exists != g.Exists || digest(b) != g.Digest {
			return nil, StoreInvalid
		}
		files = append(files, nativeFile{Name: g.Name, Exists: exists, Data: b})
	}
	if !m.validNativeSet(cli, files) {
		return nil, StoreInvalid
	}
	return files, nil
}

// saveProfile stores Pax metadata only; native files are patched when applying.
func (m *Manager) saveProfile(ctx context.Context, p savedProfile) error {
	if e := m.stateTransaction(ctx, []stateChange{change("profiles", p.ID, p)}); e != nil {
		return e
	}
	m.profiles[p.ID] = p
	return nil
}
func cloneGenerated(old map[CLI]generatedSet) map[CLI]generatedSet {
	next := map[CLI]generatedSet{}
	for k, v := range old {
		next[k] = v
	}
	return next
}
func encodeState(value any) []byte {
	b, e := json.Marshal(value)
	if e != nil {
		panic("invalid private state")
	}
	return b
}
