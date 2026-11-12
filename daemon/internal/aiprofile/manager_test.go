package aiprofile

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileSaveSurvivesRestartWithoutTouchingCLI(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "profiles")
	m, err := New(Options{Directory: dir, Home: home, Owner: uint32(os.Geteuid())})
	if err != nil {
		t.Fatal(err)
	}
	w, err := m.Mutate(context.Background(), Mutation{Action: "save", OperationID: "create", Profile: Profile{ID: "4eb87612-9a70-49d3-88e0-a54d40245269", Name: "Gateway", BaseURL: "https://api.example.test", Multiplier: 0.3}, Key: "test-private-key"})
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Profiles) != 1 || !w.Profiles[0].HasKey || w.Profiles[0].Revision != 1 {
		t.Fatalf("unexpected profile metadata: count=%d", len(w.Profiles))
	}
	if _, err = os.Stat(filepath.Join(home, ".codex")); !os.IsNotExist(err) {
		t.Fatal("saving must not initialize or change CLI configuration")
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = New(Options{Directory: dir, Home: home, Owner: uint32(os.Geteuid())})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	w, err = m.Query(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Profiles) != 1 || w.Profiles[0].Name != "Gateway" || w.Profiles[0].Multiplier != 0.3 {
		t.Fatal("saved profile was not recovered")
	}
}

func TestCodexImportAndSwitchPreserveNativeSettings(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	cli := filepath.Join(home, ".codex")
	if err := os.Mkdir(cli, 0755); err != nil {
		t.Fatal(err)
	}
	config := []byte("model = 'keep-model'\nmodel_provider = 'gateway'\n[model_providers.gateway]\nbase_url = 'https://old.example.test/v1'\nwire_api = 'responses'\n[mcp_servers.keep]\ncommand = 'keep-command'\n")
	auth := []byte(`{"OPENAI_API_KEY":"old-key","unrelated":"preserve"}`)
	for n, b := range map[string][]byte{"config.toml": config, "auth.json": auth} {
		if err := os.WriteFile(filepath.Join(cli, n), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	m, err := New(Options{Directory: filepath.Join(home, "profiles"), Home: home, Owner: uint32(os.Geteuid())})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	w, err := m.Query(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	w, err = m.Mutate(context.Background(), Mutation{Action: "import", OperationID: "import", CLI: Codex, InspectionID: w.Bindings[0].InspectionID, Profile: Profile{Name: "Original", Multiplier: 1}})
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(cli, "config.toml"))
	if err != nil || string(original) != string(config) {
		t.Fatal("import changed native configuration")
	}
	w, err = m.Mutate(context.Background(), Mutation{Action: "save", OperationID: "new", Profile: Profile{ID: "4eb87612-9a70-49d3-88e0-a54d40245269", Name: "Next", BaseURL: "https://new.example.test", Multiplier: 0.3}, Key: "new-key"})
	if err != nil {
		t.Fatal(err)
	}
	var p Profile
	for _, v := range w.Profiles {
		if v.Name == "Next" {
			p = v
		}
	}
	plan, err := m.Plan(context.Background(), PlanRequest{CLI: Codex, ProfileID: p.ID, Revision: p.Revision, TemplateRevision: w.Bindings[0].TemplateRevision, InspectionID: w.Bindings[0].InspectionID})
	if err != nil {
		t.Fatal(err)
	}
	w, err = m.Apply(context.Background(), ApplyRequest{PlanID: plan.ID, OperationID: "apply"})
	if err != nil {
		t.Fatal(err)
	}
	if w.Bindings[0].State != "applied" {
		t.Fatal("successful group not reported applied")
	}
	b, _ := os.ReadFile(filepath.Join(cli, "config.toml"))
	text := string(b)
	for _, literal := range []string{"keep-model", "keep-command", "https://new.example.test", "responses"} {
		if !strings.Contains(text, literal) {
			t.Fatalf("native setting lost: %s", literal)
		}
	}
	b, _ = os.ReadFile(filepath.Join(cli, "auth.json"))
	if !strings.Contains(string(b), "new-key") || !strings.Contains(string(b), "preserve") {
		t.Fatal("auth template not preserved")
	}
}

func TestCodexFailedSecondWriteRestoresCompleteGroup(t *testing.T) {
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
	defer func() { _ = m.Close() }()
	w, _ := m.Query(context.Background())
	w, e = m.Mutate(context.Background(), Mutation{Action: "import", OperationID: "import", CLI: Codex, InspectionID: w.Bindings[0].InspectionID, Profile: Profile{Name: "Original", Multiplier: 1}})
	if e != nil {
		t.Fatal(e)
	}
	p := w.Profiles[0]
	p.BaseURL = "https://after.test"
	w, e = m.Mutate(context.Background(), Mutation{Action: "save", OperationID: "edit", ExpectedRevision: p.Revision, Profile: p, Key: "after"})
	if e != nil {
		t.Fatal(e)
	}
	plan, e := m.Plan(context.Background(), PlanRequest{CLI: Codex, ProfileID: p.ID, Revision: w.Profiles[0].Revision, TemplateRevision: w.Bindings[0].TemplateRevision, InspectionID: w.Bindings[0].InspectionID})
	if e != nil {
		t.Fatal(e)
	}
	m.renameHook = func(index int) error {
		if index == 1 {
			return os.ErrPermission
		}
		return nil
	}
	if _, e = m.Apply(context.Background(), ApplyRequest{PlanID: plan.ID, OperationID: "fail"}); e == nil {
		t.Fatal("failure incorrectly acknowledged")
	}
	for n, want := range map[string][]byte{"config.toml": cfg, "auth.json": auth} {
		b, e := os.ReadFile(filepath.Join(dir, n))
		if e != nil || string(b) != string(want) {
			t.Fatalf("group not restored: %s", n)
		}
	}
}
