package aiprofile

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSwitchPatchesCurrentClaudeFileWithoutTemplate(t *testing.T) {
	m, home := claudeFixture(t)
	raw := `{ "env": {"ANTHROPIC_BASE_URL":"https://before.test","ANTHROPIC_AUTH_TOKEN":"before-key","OTHER":"new-value"}, "permissions":{"allow":["Read"]}, "model":"keep" }`
	path := filepath.Join(home, ".claude", "settings.json")
	if e := os.WriteFile(path, []byte(raw), 0600); e != nil {
		t.Fatal(e)
	}
	w, e := m.Mutate(context.Background(), Mutation{Action: "save", OperationID: "save", Profile: Profile{ID: newID(), Name: "Next", BaseURL: "https://next.test", Multiplier: 1}, Key: "next-key"})
	if e != nil {
		t.Fatal(e)
	}
	p := w.Profiles[0]
	plan, e := m.Plan(context.Background(), PlanRequest{CLI: Claude, ProfileID: p.ID, Revision: p.Revision, InspectionID: w.Bindings[1].InspectionID})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Apply(context.Background(), ApplyRequest{PlanID: plan.ID, OperationID: "switch"}); e != nil {
		t.Fatal(e)
	}
	want := strings.ReplaceAll(strings.ReplaceAll(raw, "https://before.test", "https://next.test"), "before-key", "next-key")
	got, _ := os.ReadFile(path)
	if string(got) != want {
		t.Fatal("switch changed content outside provider fields")
	}
}

func TestCodexSwitchPreservesMCPAndCommentsExactly(t *testing.T) {
	for _, inline := range []bool{false, true} {
		t.Run(fmt.Sprint(inline), func(t *testing.T) {
			home, _ := filepath.EvalSymlinks(t.TempDir())
			dir := filepath.Join(home, ".codex")
			if e := os.Mkdir(dir, 0700); e != nil {
				t.Fatal(e)
			}
			config := "# keep comment\nmodel_provider = 'p'\nmodel = 'keep'\n[model_providers.p]\nbase_url = 'https://before.test' # address\nwire_api = 'responses'\n[mcp_servers.latest]\ncommand = 'new-mcp'\n"
			if inline {
				config = "# keep comment\nmodel_provider='p'\nmodel_providers = {p={base_url='https://before.test',wire_api='responses'}}\n[mcp_servers.latest]\ncommand='new-mcp'\n"
			}
			auth := `{ "OPENAI_API_KEY" : "before", "unknown": {"retain":true} }`
			if e := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(config), 0600); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(auth), 0600); e != nil {
				t.Fatal(e)
			}
			m, e := New(Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid())})
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = m.Close() }()
			w, e := m.Mutate(context.Background(), Mutation{Action: "save", OperationID: "create", Profile: Profile{ID: newID(), Name: "Next", BaseURL: "https://next.test", Multiplier: 1}, Key: "next"})
			if e != nil {
				t.Fatal(e)
			}
			p := w.Profiles[0]
			plan, e := m.Plan(context.Background(), PlanRequest{CLI: Codex, ProfileID: p.ID, Revision: p.Revision, InspectionID: w.Bindings[0].InspectionID})
			if e != nil {
				t.Fatal(e)
			}
			if _, e = m.Apply(context.Background(), ApplyRequest{PlanID: plan.ID, OperationID: "apply"}); e != nil {
				t.Fatal(e)
			}
			got, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
			want := strings.Replace(config, "'https://before.test'", `"https://next.test"`, 1)
			if string(got) != want {
				t.Fatal("MCP, comments or unrelated bytes changed")
			}
			got, _ = os.ReadFile(filepath.Join(dir, "auth.json"))
			if string(got) != strings.Replace(auth, `"before"`, `"next"`, 1) {
				t.Fatal("auth unknown fields changed")
			}
		})
	}
}

func TestInvalidCurrentConfigurationNeverFallsBackToTemplate(t *testing.T) {
	for _, raw := range []string{"", "{broken", `{"env":[],"model":"keep"}`, `{"env":{"ANTHROPIC_BASE_URL":"https://x.test","ANTHROPIC_AUTH_TOKEN":"one","ANTHROPIC_API_KEY":"two"}}`, `{"env":{"ANTHROPIC_BASE_URL":"https://x.test","ANTHROPIC_AUTH_TOKEN":"one","ANTHROPIC_AUTH_TOKEN":"two"}}`} {
		t.Run(fmt.Sprint(len(raw)), func(t *testing.T) {
			m, home := claudeFixture(t)
			w := importClaude(t, m)
			p := w.Profiles[0]
			path := filepath.Join(home, ".claude", "settings.json")
			if e := os.WriteFile(path, []byte(raw), 0600); e != nil {
				t.Fatal(e)
			}
			w, e := m.Query(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			if _, e = m.Plan(context.Background(), PlanRequest{CLI: Claude, ProfileID: p.ID, Revision: p.Revision, InspectionID: w.Bindings[1].InspectionID}); e == nil {
				t.Fatal("invalid current document accepted")
			}
			got, _ := os.ReadFile(path)
			if string(got) != raw {
				t.Fatal("invalid document overwritten")
			}
		})
	}
}

func TestSwitchAfterPlanRejectsNewContent(t *testing.T) {
	m, home := claudeFixture(t)
	w := importClaude(t, m)
	p := w.Profiles[0]
	plan, e := m.Plan(context.Background(), PlanRequest{CLI: Claude, ProfileID: p.ID, Revision: p.Revision, InspectionID: w.Bindings[1].InspectionID})
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(home, ".claude", "settings.json")
	raw, _ := os.ReadFile(path)
	raw = append(raw, ' ')
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Apply(context.Background(), ApplyRequest{PlanID: plan.ID, OperationID: "stale"}); e != Changed {
		t.Fatal("concurrent edit overwritten")
	}
}

func TestDSHCurrentAPIFilesKeepCommentsAndAccountRecords(t *testing.T) {
	for _, desktop := range []bool{false, true} {
		t.Run(fmt.Sprint(desktop), func(t *testing.T) {
			home, _ := filepath.EvalSymlinks(t.TempDir())
			dir := filepath.Join(home, ".dsh")
			if e := os.MkdirAll(filepath.Join(dir, "profiles", "desktop"), 0700); e != nil {
				t.Fatal(e)
			}
			version := "0.1.5-rc.1"
			name := "settings.yaml"
			raw := "# user settings\nagent-default-model: {provider: deepseek-official, model: keep}\nllm-deepseek: {baseURL: 'https://before.test', apiKeyEnv: PAX_KEY} # keep inline\nplugins: {mcp: keep-mcp}\n"
			if desktop {
				version = "0.2.1-rc.3"
				name = "profiles/desktop/cordis.patch.yml"
				raw = "# desktop settings\n- name: '@deepseek-ai/dsh-agent-default-model'\n  config: {provider: deepseek-official, model: keep}\n- name: '@deepseek-ai/dsh-llm-deepseek-api-key'\n  config: {baseURL: 'https://before.test', apiKeyEnv: PAX_KEY} # keep inline\n- name: mcp\n  config: {command: keep-mcp}\n"
			}
			creds := "# preserve account\nversion: 1\nrefs: {PAX_KEY: 'before-key', OTHER: preserve}\nrecords: {account: preserve-account}\n"
			if e := os.WriteFile(filepath.Join(dir, name), []byte(raw), 0600); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(filepath.Join(dir, ".credentials.yaml"), []byte(creds), 0600); e != nil {
				t.Fatal(e)
			}
			m, e := New(Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid()), DSHVersion: version})
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = m.Close() }()
			w, e := m.Mutate(context.Background(), Mutation{Action: "save", OperationID: "create", Profile: Profile{ID: newID(), Name: "Next", BaseURL: "https://after.test", Multiplier: 1}, Key: "after-key"})
			if e != nil {
				t.Fatal(e)
			}
			p := w.Profiles[0]
			plan, e := m.Plan(context.Background(), PlanRequest{CLI: DSH, ProfileID: p.ID, Revision: p.Revision, InspectionID: w.Bindings[2].InspectionID})
			if e != nil {
				t.Fatal(e)
			}
			if _, e = m.Apply(context.Background(), ApplyRequest{PlanID: plan.ID, OperationID: "apply"}); e != nil {
				t.Fatal(e)
			}
			got, _ := os.ReadFile(filepath.Join(dir, name))
			if string(got) != strings.Replace(raw, "'https://before.test'", `"https://after.test"`, 1) {
				t.Fatal("settings outside address changed")
			}
			got, _ = os.ReadFile(filepath.Join(dir, ".credentials.yaml"))
			if string(got) != strings.Replace(creds, "'before-key'", `"after-key"`, 1) {
				t.Fatal("account or other credentials changed")
			}
		})
	}
}

func TestMissingFileCannotBeCreatedBySwitch(t *testing.T) {
	m, home := claudeFixture(t)
	w := importClaude(t, m)
	p := w.Profiles[0]
	path := filepath.Join(home, ".claude", "settings.json")
	if e := os.Remove(path); e != nil {
		t.Fatal(e)
	}
	w, e := m.Query(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Plan(context.Background(), PlanRequest{CLI: Claude, ProfileID: p.ID, Revision: p.Revision, InspectionID: w.Bindings[1].InspectionID}); e == nil {
		t.Fatal("missing file initialized")
	}
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("missing file recreated")
	}
}

func TestCodexAmbiguousAuthRoutesRefuseProviderSwitch(t *testing.T) {
	for _, route := range []string{"oauth", "header", "env-header", "shared-provider"} {
		t.Run(route, func(t *testing.T) {
			home, _ := filepath.EvalSymlinks(t.TempDir())
			dir := filepath.Join(home, ".codex")
			if e := os.Mkdir(dir, 0700); e != nil {
				t.Fatal(e)
			}
			raw := "model_provider='p'\n[model_providers.p]\nbase_url='https://before.test'\n"
			auth := `{"OPENAI_API_KEY":"before"}`
			switch route {
			case "oauth":
				auth = `{"auth_mode":"chatgpt","tokens":{"access_token":"account"},"OPENAI_API_KEY":"before"}`
			case "header":
				raw += "http_headers={authorization='other'}\n"
			case "env-header":
				raw += "env_http_headers={aUtHoRiZaTiOn='OTHER_KEY'}\n"
			case "shared-provider":
				raw += "[profiles.shared]\nmodel_provider='p'\n"
			}
			if e := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(raw), 0600); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(auth), 0600); e != nil {
				t.Fatal(e)
			}
			m, e := New(Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid())})
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = m.Close() }()
			w, e := m.Mutate(context.Background(), Mutation{Action: "save", OperationID: "create", Profile: Profile{ID: newID(), Name: "Next", BaseURL: "https://after.test", Multiplier: 1}, Key: "after"})
			if e != nil {
				t.Fatal(e)
			}
			p := w.Profiles[0]
			if _, e = m.Plan(context.Background(), PlanRequest{CLI: Codex, ProfileID: p.ID, Revision: p.Revision, InspectionID: w.Bindings[0].InspectionID}); e == nil {
				t.Fatal("ambiguous auth accepted")
			}
			got, _ := os.ReadFile(filepath.Join(dir, "auth.json"))
			if string(got) != auth {
				t.Fatal("credentials overwritten")
			}
		})
	}
}

func TestDSHSharedKeyOrUnsupportedCredentialVersionCannotSwitch(t *testing.T) {
	for _, variant := range []string{"shared", "version"} {
		t.Run(variant, func(t *testing.T) {
			home, _ := filepath.EvalSymlinks(t.TempDir())
			dir := filepath.Join(home, ".dsh")
			if e := os.Mkdir(dir, 0700); e != nil {
				t.Fatal(e)
			}
			raw := "agent-default-model: {provider: deepseek-official, model: keep}\nllm-deepseek: {baseURL: 'https://before.test'}\nother: {apiKeyEnv: DEEPSEEK_API_KEY}\n"
			creds := "version: 1\nrefs: {DEEPSEEK_API_KEY: before}\n"
			if variant == "version" {
				raw = strings.Replace(raw, "other: {apiKeyEnv: DEEPSEEK_API_KEY}\n", "", 1)
				creds = strings.Replace(creds, "version: 1", "version: 2", 1)
			}
			if e := os.WriteFile(filepath.Join(dir, "settings.yaml"), []byte(raw), 0600); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(filepath.Join(dir, ".credentials.yaml"), []byte(creds), 0600); e != nil {
				t.Fatal(e)
			}
			m, e := New(Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid()), DSHVersion: "0.1.5-rc.1"})
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = m.Close() }()
			w, e := m.Mutate(context.Background(), Mutation{Action: "save", OperationID: "create", Profile: Profile{ID: newID(), Name: "Next", BaseURL: "https://after.test", Multiplier: 1}, Key: "after"})
			if e != nil {
				t.Fatal(e)
			}
			p := w.Profiles[0]
			if _, e = m.Plan(context.Background(), PlanRequest{CLI: DSH, ProfileID: p.ID, Revision: p.Revision, InspectionID: w.Bindings[2].InspectionID}); e == nil {
				t.Fatal("shared key accepted")
			}
			got, _ := os.ReadFile(filepath.Join(dir, ".credentials.yaml"))
			if string(got) != creds {
				t.Fatal("shared key overwritten")
			}
		})
	}
}
