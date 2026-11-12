package aiprofile

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func firstAPIManager(t *testing.T, cli CLI, initial map[string]string) (*Manager, string, Profile) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "."+string(cli))
	if len(initial) > 0 {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		for name, raw := range initial {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	m, err := New(Options{Directory: filepath.Join(home, "PaxProfiles"), Home: home, Owner: uint32(os.Geteuid()), UsageOnlyDSH: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	w, err := m.Mutate(context.Background(), Mutation{Action: "save", OperationID: "save", Profile: Profile{ID: "4eb87612-9a70-49d3-88e0-a54d40245269", Name: "API", BaseURL: "https://example.test/v1", Multiplier: 1}, Key: "fixture-key"})
	if err != nil {
		t.Fatal(err)
	}
	return m, dir, w.Profiles[0]
}
func bindingFor(t *testing.T, m *Manager, cli CLI) Binding {
	t.Helper()
	w, err := m.Query(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range w.Bindings {
		if b.CLI == cli {
			return b
		}
	}
	t.Fatal("missing binding")
	return Binding{}
}
func firstPlan(t *testing.T, m *Manager, cli CLI, p Profile) (Plan, error) {
	t.Helper()
	b := bindingFor(t, m, cli)
	return m.Plan(context.Background(), PlanRequest{CLI: cli, ProfileID: p.ID, Revision: p.Revision, InspectionID: b.InspectionID, Initialize: true, ModelID: "fixture-model"})
}
func TestFirstAPICreationAndRestart(t *testing.T) {
	for _, cli := range []CLI{Claude, Codex} {
		t.Run(string(cli), func(t *testing.T) {
			m, dir, p := firstAPIManager(t, cli, nil)
			if b := bindingFor(t, m, cli); b.State != "missing" {
				t.Fatalf("state %s", b.State)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("save or query created CLI directory")
			}
			plan, err := firstPlan(t, m, cli, p)
			if err != nil {
				t.Fatal(err)
			}
			if !plan.Initializes || plan.TargetPath != dir {
				t.Fatal("missing creation preview")
			}
			if _, err = m.Apply(context.Background(), ApplyRequest{PlanID: plan.ID, OperationID: "apply"}); err != nil {
				t.Fatal(err)
			}
			if b := bindingFor(t, m, cli); b.State != "applied" {
				t.Fatalf("state %s", b.State)
			}
			files, err := m.native(cli)
			if err != nil {
				t.Fatal(err)
			}
			_, v, err := transform(cli, files, "", "")
			if err != nil || v.url != p.BaseURL || v.key != "fixture-key" {
				t.Fatal("created configuration cannot be read")
			}
			for _, f := range files {
				info, err := os.Stat(filepath.Join(dir, f.Name))
				if err != nil || info.Mode().Perm() != 0600 {
					t.Fatal("private file permissions")
				}
			}
			opts := Options{Directory: m.dir, Home: m.home, Owner: m.owner, UsageOnlyDSH: true}
			if err = m.Close(); err != nil {
				t.Fatal(err)
			}
			next, err := New(opts)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = next.Close() }()
			if b := bindingFor(t, next, cli); b.State != "applied" {
				t.Fatal("binding not durable")
			}
		})
	}
}
func TestFirstAPIPreservesExistingPreferences(t *testing.T) {
	for _, cli := range []CLI{Claude, Codex} {
		t.Run(string(cli), func(t *testing.T) {
			name, raw := "settings.json", "{\n  \"permissions\": {\"allow\":[\"Read\"]},\n  \"env\": {\"KEEP\":\"value\"}\n}\n"
			if cli == Codex {
				name = "config.toml"
				raw = "# preserve comment\nmodel = 'keep-model'\n[mcp_servers.keep]\ncommand = 'keep-command'\n"
			}
			m, dir, p := firstAPIManager(t, cli, map[string]string{name: raw})
			plan, err := firstPlan(t, m, cli, p)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.Apply(context.Background(), ApplyRequest{PlanID: plan.ID, OperationID: "apply"}); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			for _, part := range []string{"\"permissions\": {\"allow\":[\"Read\"]}", "\"KEEP\":\"value\""} {
				if cli == Claude && !strings.Contains(string(b), part) {
					t.Fatal("lost settings bytes")
				}
			}
			if cli == Codex && !strings.Contains(string(b), raw) {
				t.Fatal("changed existing model/MCP bytes")
			}
		})
	}
}
func TestFirstAPIRejectsAccountsAndMalformedTargets(t *testing.T) {
	cases := []struct {
		cli   CLI
		files map[string]string
		state string
	}{
		{Codex, map[string]string{"auth.json": `{"tokens":{"access_token":"fixture"}}`}, "account"},
		{Codex, map[string]string{"config.toml": "bad = ["}, "invalid"},
		{Claude, map[string]string{"settings.json": `{"env":{"ANTHROPIC_API_KEY":"existing"}}`}, "invalid"},
		{Claude, map[string]string{"settings.json": `{"apiKeyHelper":"command"}`}, "account"},
		{Claude, map[string]string{".credentials.json": `{"claudeAiOauth":{}}`}, "account"},
	}
	for _, c := range cases {
		t.Run(string(c.cli)+c.state, func(t *testing.T) {
			m, dir, p := firstAPIManager(t, c.cli, c.files)
			if b := bindingFor(t, m, c.cli); b.State != c.state {
				t.Fatalf("state %s", b.State)
			}
			if _, err := firstPlan(t, m, c.cli, p); err == nil {
				t.Fatal("accepted unsafe initialization")
			}
			for n, want := range c.files {
				b, err := os.ReadFile(filepath.Join(dir, n))
				if err != nil || string(b) != want {
					t.Fatal("changed existing target")
				}
			}
		})
	}
}
func TestFirstAPIRejectsExternalCreation(t *testing.T) {
	m, dir, p := firstAPIManager(t, Codex, nil)
	plan, err := firstPlan(t, m, Codex, p)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	raw := []byte("model = 'external'\n")
	if err = os.WriteFile(filepath.Join(dir, "config.toml"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), ApplyRequest{PlanID: plan.ID, OperationID: "apply"}); err != Changed {
		t.Fatalf("want changed, got %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	if string(b) != string(raw) {
		t.Fatal("overwrote external file")
	}
}
func TestFirstAPIInterruptedMultiFileRestores(t *testing.T) {
	m, dir, p := firstAPIManager(t, Codex, nil)
	plan, err := firstPlan(t, m, Codex, p)
	if err != nil {
		t.Fatal(err)
	}
	m.renameHook = func(i int) error {
		if i == 1 {
			return Invalid
		}
		return nil
	}
	if _, err = m.Apply(context.Background(), ApplyRequest{PlanID: plan.ID, OperationID: "apply"}); err != Invalid {
		t.Fatalf("unexpected result %v", err)
	}
	for _, n := range []string{"auth.json", "config.toml"} {
		if _, err = os.Stat(filepath.Join(dir, n)); !os.IsNotExist(err) {
			t.Fatal("partial initialization left a file")
		}
	}
	if m.recovery.Pending != nil || m.blocked {
		t.Fatal("failed to recover")
	}
}

func TestFirstAPIStopsFileCreationRaceAtCommit(t *testing.T) {
	m, dir, p := firstAPIManager(t, Codex, nil)
	plan, err := firstPlan(t, m, Codex, p)
	if err != nil {
		t.Fatal(err)
	}
	m.renameHook = func(i int) error {
		if i == 0 {
			return os.WriteFile(filepath.Join(dir, "config.toml"), []byte("external-owned\n"), 0600)
		}
		return nil
	}
	if _, err = m.Apply(context.Background(), ApplyRequest{PlanID: plan.ID, OperationID: "race"}); err != RecoveryRequired {
		t.Fatalf("want recovery, got %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil || string(b) != "external-owned\n" {
		t.Fatal("overwrote external creation")
	}
	if _, err = os.Stat(filepath.Join(dir, "auth.json")); !os.IsNotExist(err) {
		t.Fatal("continued writing after collision")
	}
}
func TestFirstClaudeAccountMarkerPreventsInitialization(t *testing.T) {
	m, _, p := firstAPIManager(t, Claude, nil)
	if err := os.WriteFile(filepath.Join(m.home, ".claude.json"), []byte(`{"oauthAccount":{"emailAddress":"fixture@example.test"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if b := bindingFor(t, m, Claude); b.State != "account" {
		t.Fatalf("state %s", b.State)
	}
	if _, err := firstPlan(t, m, Claude, p); err == nil {
		t.Fatal("accepted account conversion")
	}
}
func TestFirstCodexNeedsExplicitModel(t *testing.T) {
	m, _, p := firstAPIManager(t, Codex, nil)
	b := bindingFor(t, m, Codex)
	if _, err := m.Plan(context.Background(), PlanRequest{CLI: Codex, ProfileID: p.ID, Revision: p.Revision, InspectionID: b.InspectionID, Initialize: true}); err != Invalid {
		t.Fatalf("missing model accepted: %v", err)
	}
}

func TestFirstClaudeCustomDirectoryAccountMarker(t *testing.T) {
	m, dir, p := firstAPIManager(t, Claude, map[string]string{".claude.json": `{"oauthAccount":{"emailAddress":"fixture@example.test"}}`})
	m.directories = map[CLI]string{Claude: dir}
	if b := bindingFor(t, m, Claude); b.State != "account" {
		t.Fatalf("custom account state %s", b.State)
	}
	if _, err := firstPlan(t, m, Claude, p); err == nil {
		t.Fatal("converted custom directory account")
	}
}

func TestBindingDoesNotReturnURLCredentials(t *testing.T) {
	m, _, _ := firstAPIManager(t, Claude, map[string]string{"settings.json": `{"env":{"ANTHROPIC_BASE_URL":"https://person:secret@example.test/v1?key=secret","ANTHROPIC_AUTH_TOKEN":"fixture-key"}}`})
	b := bindingFor(t, m, Claude)
	if b.State != "invalid" || b.Endpoint != "" {
		t.Fatal("returned credential-bearing endpoint")
	}
}
