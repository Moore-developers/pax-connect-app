package aiprofile

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func claudeFixture(t *testing.T) (*Manager, string) {
	t.Helper()
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(home, ".claude")
	if e = os.Mkdir(dir, 0755); e != nil {
		t.Fatal(e)
	}
	raw := `{"model":"native-model","permissions":{"allow":["Read"]},"env":{"ANTHROPIC_BASE_URL":"https://before.test","ANTHROPIC_AUTH_TOKEN":"before-key","OTHER":"keep"}}`
	if e = os.WriteFile(filepath.Join(dir, "settings.json"), []byte(raw), 0600); e != nil {
		t.Fatal(e)
	}
	m, e := New(Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid())})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m, home
}
func importClaude(t *testing.T, m *Manager) Workspace {
	t.Helper()
	w, e := m.Query(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	w, e = m.Mutate(context.Background(), Mutation{Action: "import", CLI: Claude, InspectionID: w.Bindings[1].InspectionID, OperationID: "import", Profile: Profile{Name: "Original", Multiplier: 1}})
	if e != nil {
		t.Fatal(e)
	}
	return w
}
func TestClaudeSwitchPreservesLatestSettingsWithoutCreatingSnapshot(t *testing.T) {
	m, home := claudeFixture(t)
	w := importClaude(t, m)
	p := w.Profiles[0]
	p.BaseURL = "https://next.test"
	w, e := m.Mutate(context.Background(), Mutation{Action: "save", OperationID: "edit", Profile: p, ExpectedRevision: p.Revision, Key: "next-key"})
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(home, ".claude", "settings.json")
	b, _ := os.ReadFile(path)
	if e = os.WriteFile(path, []byte(strings.ReplaceAll(string(b), "keep", "external-new")), 0600); e != nil {
		t.Fatal(e)
	}
	w, e = m.Query(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	count := len(w.Profiles)
	plan, e := m.Plan(context.Background(), PlanRequest{CLI: Claude, ProfileID: p.ID, Revision: w.Profiles[0].Revision, TemplateRevision: w.Bindings[1].TemplateRevision, InspectionID: w.Bindings[1].InspectionID})
	if e != nil {
		t.Fatal(e)
	}
	w, e = m.Apply(context.Background(), ApplyRequest{PlanID: plan.ID, OperationID: "switch"})
	if e != nil {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(path)
	if !strings.Contains(string(b), "next-key") || !strings.Contains(string(b), "https://next.test") || !strings.Contains(string(b), "external-new") {
		t.Fatal("latest settings or selected provider lost")
	}
	if len(w.Profiles) != count || w.Bindings[1].State != "applied" {
		t.Fatal("switch created a snapshot or failed to mark selected Profile")
	}
	if _, e = m.Apply(context.Background(), ApplyRequest{PlanID: plan.ID, OperationID: "switch"}); e != nil {
		t.Fatal("completed switch not idempotent")
	}
}
func TestNoSecretInProjectionAndMetadataEditDoesNotRequireApply(t *testing.T) {
	m, _ := claudeFixture(t)
	w := importClaude(t, m)
	p := w.Profiles[0]
	plan, e := m.Plan(context.Background(), PlanRequest{CLI: Claude, ProfileID: p.ID, Revision: p.Revision, TemplateRevision: w.Bindings[1].TemplateRevision, InspectionID: w.Bindings[1].InspectionID})
	if e != nil {
		t.Fatal(e)
	}
	w, e = m.Apply(context.Background(), ApplyRequest{PlanID: plan.ID, OperationID: "apply"})
	if e != nil {
		t.Fatal(e)
	}
	p.Name = "Renamed"
	p.Multiplier = 0.3
	w, e = m.Mutate(context.Background(), Mutation{Action: "save", OperationID: "metadata", ExpectedRevision: p.Revision, Profile: p})
	if e != nil {
		t.Fatal(e)
	}
	if w.Bindings[1].State != "applied" {
		t.Fatal("metadata edit invalidated connection")
	}
	b, _ := json.Marshal(w)
	if strings.Contains(string(b), "before-key") {
		t.Fatal("secret escaped workspace")
	}
	for _, kind := range []string{"templates"} {
		data, _ := os.ReadFile(filepath.Join(m.dir, kind, "claude.json"))
		if strings.Contains(string(data), "before-key") {
			t.Fatal("template contains managed secret")
		}
	}
}
func TestDuplicateJSONAndSymlinkAreRejected(t *testing.T) {
	m, home := claudeFixture(t)
	p := filepath.Join(home, ".claude", "settings.json")
	if e := os.WriteFile(p, []byte(`{"env":{},"env":{"ANTHROPIC_BASE_URL":"https://x.test","ANTHROPIC_AUTH_TOKEN":"key"}}`), 0600); e != nil {
		t.Fatal(e)
	}
	w, _ := m.Query(context.Background())
	if _, e := m.Mutate(context.Background(), Mutation{Action: "import", OperationID: "dup", CLI: Claude, InspectionID: w.Bindings[1].InspectionID, Profile: Profile{Name: "Bad", Multiplier: 1}}); e == nil {
		t.Fatal("duplicate object keys accepted")
	}
	if e := os.Remove(p); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(filepath.Join(home, "foreign"), p); e != nil {
		t.Fatal(e)
	}
	w, e := m.Query(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if w.Bindings[1].Reason != string(Unsafe) {
		t.Fatal("symlink not rejected")
	}
}
func TestDSHAccountConfigurationCannotBeInitializedOrOverwritten(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	dir := filepath.Join(home, ".dsh", "profiles", "desktop")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	patch := []byte("- name: '@deepseek-ai/dsh-agent-default-model'\n  config:\n    provider: deepseek-account\n    model: native-model\n")
	if e := os.WriteFile(filepath.Join(dir, "cordis.patch.yml"), patch, 0600); e != nil {
		t.Fatal(e)
	}
	creds := []byte("version: 1\nrefs: {}\nrecords: {account: preserved}\n")
	if e := os.WriteFile(filepath.Join(home, ".dsh", ".credentials.yaml"), creds, 0600); e != nil {
		t.Fatal(e)
	}
	m, e := New(Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid()), DSHVersion: "0.2.1-rc.3"})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = m.Close() }()
	w, _ := m.Query(context.Background())
	if _, e = m.Mutate(context.Background(), Mutation{Action: "initialize_template", CLI: DSH, InspectionID: w.Bindings[2].InspectionID, OperationID: "init"}); e != Unsupported {
		t.Fatal("initialization should not rewrite existing configurations")
	}
	got, _ := os.ReadFile(filepath.Join(dir, "cordis.patch.yml"))
	if string(got) != string(patch) {
		t.Fatal("account settings changed")
	}
}
func TestInterruptedCodexGroupRecoversOnRestart(t *testing.T) {
	if home := os.Getenv("PAX_AI_CRASH_HOME"); home != "" {
		m, e := New(Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid())})
		if e != nil {
			os.Exit(11)
		}
		w, _ := m.Query(context.Background())
		p := w.Profiles[0]
		plan, e := m.Plan(context.Background(), PlanRequest{CLI: Codex, ProfileID: p.ID, Revision: p.Revision, TemplateRevision: w.Bindings[0].TemplateRevision, InspectionID: w.Bindings[0].InspectionID})
		if e != nil {
			os.Exit(12)
		}
		m.renameHook = func(i int) error {
			if i == 1 {
				os.Exit(29)
			}
			return nil
		}
		_, _ = m.Apply(context.Background(), ApplyRequest{PlanID: plan.ID, OperationID: "crash"})
		os.Exit(13)
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	dir := filepath.Join(home, ".codex")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	cfg := []byte("model_provider='p'\n[model_providers.p]\nbase_url='https://before.test'\n")
	auth := []byte(`{"OPENAI_API_KEY":"before"}`)
	for n, b := range map[string][]byte{"config.toml": cfg, "auth.json": auth} {
		if e := os.WriteFile(filepath.Join(dir, n), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	m, e := New(Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid())})
	if e != nil {
		t.Fatal(e)
	}
	w, _ := m.Query(context.Background())
	w, e = m.Mutate(context.Background(), Mutation{Action: "import", OperationID: "import", CLI: Codex, InspectionID: w.Bindings[0].InspectionID, Profile: Profile{Name: "Original", Multiplier: 1}})
	if e != nil {
		t.Fatal(e)
	}
	p := w.Profiles[0]
	p.BaseURL = "https://after.test"
	_, e = m.Mutate(context.Background(), Mutation{Action: "save", OperationID: "edit", Profile: p, ExpectedRevision: p.Revision, Key: "after"})
	if e != nil {
		t.Fatal(e)
	}
	_ = m.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestInterruptedCodexGroupRecoversOnRestart$")
	cmd.Env = append(os.Environ(), "PAX_AI_CRASH_HOME="+home)
	if e = cmd.Run(); e == nil {
		t.Fatal("child did not interrupt transaction")
	}
	m, e = New(Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid())})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = m.Close() }()
	for n, want := range map[string][]byte{"config.toml": cfg, "auth.json": auth} {
		b, _ := os.ReadFile(filepath.Join(dir, n))
		if string(b) != string(want) {
			t.Fatal("restart left a mixed configuration")
		}
	}
}

func TestFIFOIsRejectedWithoutBlockingOwner(t *testing.T) {
	m, home := claudeFixture(t)
	path := filepath.Join(home, ".claude", "settings.json")
	if e := os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e := syscall.Mkfifo(path, 0600); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := m.Query(context.Background()); done <- e }()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO blocked owner")
	}
	w, e := m.Query(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if w.Bindings[1].Reason != string(Unsafe) {
		t.Fatal("FIFO accepted as configuration")
	}
}
func TestUnknownDSHVersionCannotImportExistingAPIConfiguration(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	dir := filepath.Join(home, ".dsh")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "settings.yaml"), []byte("agent-default-model:\n  provider: deepseek-official\nllm-deepseek:\n  baseURL: https://api.test\n  apiKeyEnv: KEY\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, ".credentials.yaml"), []byte("version: 1\nrefs:\n  KEY: secret\n"), 0600); e != nil {
		t.Fatal(e)
	}
	m, e := New(Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid()), DSHVersion: "unknown"})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = m.Close() }()
	w, _ := m.Query(context.Background())
	_, e = m.Mutate(context.Background(), Mutation{Action: "import", OperationID: "import", CLI: DSH, InspectionID: w.Bindings[2].InspectionID, Profile: Profile{Name: "Gateway", Multiplier: 1}})
	if !errors.Is(e, Unsupported) {
		t.Fatal("unverified DSH version imported")
	}
	if _, e = m.Plan(context.Background(), PlanRequest{CLI: DSH}); !errors.Is(e, Unsupported) {
		t.Fatal("unverified DSH version planned")
	}
}

func TestTargetReplacedWithIdenticalContentInvalidatesPlan(t *testing.T) {
	m, home := claudeFixture(t)
	w := importClaude(t, m)
	p := w.Profiles[0]
	plan, e := m.Plan(context.Background(), PlanRequest{CLI: Claude, ProfileID: p.ID, Revision: p.Revision, TemplateRevision: w.Bindings[1].TemplateRevision, InspectionID: w.Bindings[1].InspectionID})
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(home, ".claude", "settings.json")
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	next := path + ".external"
	if e = os.WriteFile(next, b, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(next, path); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Apply(context.Background(), ApplyRequest{PlanID: plan.ID, OperationID: "must-conflict"}); !errors.Is(e, Changed) {
		t.Fatal("new file identity was not detected")
	}
}
