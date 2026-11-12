package aiprofile

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileSaveAndImportDoNotGenerateNativeCopies(t *testing.T) {
	m, home := claudeFixture(t)
	w := importClaude(t, m)
	p := w.Profiles[0]
	dir := filepath.Join(home, "state", "profiles", p.ID)
	entries, e := os.ReadDir(dir)
	if e != nil || len(entries) != 1 || entries[0].Name() != "profile.json" {
		t.Fatal("import generated native copies")
	}
	p.BaseURL = "https://updated.test"
	if _, e = m.Mutate(context.Background(), Mutation{Action: "save", OperationID: "edit", ExpectedRevision: p.Revision, Profile: p, Key: "updated-key"}); e != nil {
		t.Fatal(e)
	}
	entries, _ = os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal("save generated native copies")
	}
	native, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if strings.Contains(string(native), "updated-key") {
		t.Fatal("save modified CLI")
	}
}
func TestLegacyTemplatesCannotOverrideCurrentConfiguration(t *testing.T) {
	m, home := claudeFixture(t)
	w := importClaude(t, m)
	p := w.Profiles[0]
	writeLegacy(t, m.dir, "templates/claude.json", map[string]any{"schema": 999, "files": "broken legacy template"})
	w, e := m.Query(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	plan, e := m.Plan(context.Background(), PlanRequest{CLI: Claude, ProfileID: p.ID, Revision: p.Revision, InspectionID: w.Bindings[1].InspectionID})
	if e != nil {
		t.Fatal(e)
	}
	before, _ := os.Stat(filepath.Join(home, ".claude", "settings.json"))
	if _, e = m.Apply(context.Background(), ApplyRequest{PlanID: plan.ID, OperationID: "no-op"}); e != nil {
		t.Fatal(e)
	}
	after, _ := os.Stat(filepath.Join(home, ".claude", "settings.json"))
	if !os.SameFile(before, after) {
		t.Fatal("identical settings rewritten")
	}
}
func TestFailedProfileSaveKeepsPreviousConnection(t *testing.T) {
	m, _ := claudeFixture(t)
	w := importClaude(t, m)
	p := w.Profiles[0]
	old := p.BaseURL
	p.BaseURL = "https://updated.test"
	m.stateWriteHook = func(int) error { return os.ErrPermission }
	if _, e := m.Mutate(context.Background(), Mutation{Action: "save", OperationID: "failed", ExpectedRevision: p.Revision, Profile: p, Key: "new"}); e == nil {
		t.Fatal("failure ignored")
	}
	m.stateWriteHook = nil
	w, e := m.Query(context.Background())
	if e != nil || w.Profiles[0].BaseURL != old {
		t.Fatal("partial metadata saved")
	}
}
func TestDSHInitializationAtProfileLimitLeavesStoreUsable(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(home, "state")
	dir := filepath.Join(home, ".dsh", "profiles", "desktop")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	patch := []byte("- name: '@deepseek-ai/dsh-agent-default-model'\n  config:\n    provider: deepseek-account\n    model: native-model\n")
	if e := os.WriteFile(filepath.Join(dir, "cordis.patch.yml"), patch, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(home, ".dsh", ".credentials.yaml"), []byte("version: 1\nrefs: {}\nrecords: {}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	m, e := New(Options{Directory: root, Home: home, Owner: uint32(os.Geteuid()), DSHVersion: "0.2.1-rc.3"})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 256; i++ {
		_, e = m.Mutate(context.Background(), Mutation{Action: "save", OperationID: fmt.Sprint(i), Profile: Profile{ID: fmt.Sprintf("%032x", i+1), Name: fmt.Sprintf("Profile %03d", i), BaseURL: "https://api.test", Multiplier: 1}, Key: "test-key"})
		if e != nil {
			t.Fatal(e)
		}
	}
	w, e := m.Query(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Mutate(context.Background(), Mutation{Action: "initialize_template", CLI: DSH, InspectionID: w.Bindings[2].InspectionID, OperationID: "no-room"}); e != Unsupported {
		t.Fatal("DSH added a 257th profile")
	}
	_ = m.Close()
	m, e = New(Options{Directory: root, Home: home, Owner: uint32(os.Geteuid()), DSHVersion: "0.2.1-rc.3"})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = m.Close() }()
	w, e = m.Query(context.Background())
	if e != nil || len(w.Profiles) != 256 {
		t.Fatal("failed initialization corrupted store")
	}
}

func TestFinderMetadataDoesNotDisableProfileLoading(t *testing.T) {
	m, home := claudeFixture(t)
	w := importClaude(t, m)
	if e := m.Close(); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(home, "state", "profiles", ".DS_Store"), []byte("Finder metadata"), 0600); e != nil {
		t.Fatal(e)
	}
	restored, e := New(Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid())})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = restored.Close() }()
	next, e := restored.Query(context.Background())
	if e != nil || len(next.Profiles) != len(w.Profiles) {
		t.Fatal("opening the profile folder in Finder disabled configuration")
	}
}

func TestCodexApplicationStatusIgnoresUnrelatedMCPChanges(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	dir := filepath.Join(home, ".codex")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	config := []byte("model='native-model'\nmodel_provider='p'\n[model_providers.p]\nbase_url='https://krill.test/v1'\n")
	if e := os.WriteFile(filepath.Join(dir, "config.toml"), config, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"OPENAI_API_KEY":"krill-key"}`), 0600); e != nil {
		t.Fatal(e)
	}
	m, e := New(Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid())})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = m.Close() }()
	w, _ := m.Query(context.Background())
	w, e = m.Mutate(context.Background(), Mutation{Action: "import", OperationID: "import", CLI: Codex, InspectionID: w.Bindings[0].InspectionID, Profile: Profile{Name: "Krill", Multiplier: 1}})
	if e != nil {
		t.Fatal(e)
	}
	changed := append(config, []byte("[mcp_servers.new]\ncommand='new-command'\n")...)
	if e = os.WriteFile(filepath.Join(dir, "config.toml"), changed, 0600); e != nil {
		t.Fatal(e)
	}
	w, e = m.Query(context.Background())
	if e != nil || w.Bindings[0].State != "applied" {
		t.Fatal("unrelated MCP update misreported provider as not applied")
	}
	if e = os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"OPENAI_API_KEY":"different"}`), 0600); e != nil {
		t.Fatal(e)
	}
	w, e = m.Query(context.Background())
	if e != nil || w.Bindings[0].State != "changed" {
		t.Fatal("credential difference ignored")
	}
}
