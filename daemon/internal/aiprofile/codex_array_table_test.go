package aiprofile

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Codex keeps valid array tables alongside provider settings. Planning must not
// panic or write files; applying must preserve everything outside URL and key.
func TestCodexSwitchPreservesArrayTables(t *testing.T) {
	for name, extra := range map[string]string{
		"array":  "[[agents]]\nname = 'keep'\n",
		"nested": "[[agents]]\nname = 'keep'\n[[agents.rules]]\ncommand = 'untouched'\n",
	} {
		t.Run(name, func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(home, ".codex")
			if err = os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			config := "# user settings\nmodel = 'keep-model'\nmodel_provider = 'p'\n[model_providers.p]\nbase_url = 'https://before.test' # provider\nwire_api = 'responses'\n[mcp_servers.keep]\ncommand = 'keep-command'\n" + extra
			auth := `{"OPENAI_API_KEY":"before-key","unrelated":"keep"}`
			for n, raw := range map[string]string{"config.toml": config, "auth.json": auth} {
				if err = os.WriteFile(filepath.Join(dir, n), []byte(raw), 0600); err != nil {
					t.Fatal(err)
				}
			}
			m, err := New(Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid())})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = m.Close() }()
			w, err := m.Mutate(context.Background(), Mutation{Action: "save", OperationID: "save", Profile: Profile{ID: newID(), Name: "Next", BaseURL: "https://next.test", Multiplier: 1}, Key: "next-key"})
			if err != nil {
				t.Fatal(err)
			}
			p := w.Profiles[0]
			plan, err := m.Plan(context.Background(), PlanRequest{CLI: Codex, ProfileID: p.ID, Revision: p.Revision, InspectionID: w.Bindings[0].InspectionID})
			if err != nil {
				t.Fatal(err)
			}
			check := func(name, want string) {
				t.Helper()
				got, e := os.ReadFile(filepath.Join(dir, name))
				if e != nil {
					t.Fatal(e)
				}
				if string(got) != want {
					t.Fatalf("%s changed unexpectedly", name)
				}
			}
			check("config.toml", config)
			check("auth.json", auth)
			if _, err = m.Apply(context.Background(), ApplyRequest{PlanID: plan.ID, OperationID: "apply"}); err != nil {
				t.Fatal(err)
			}
			check("config.toml", strings.Replace(config, "'https://before.test'", `"https://next.test"`, 1))
			check("auth.json", strings.Replace(auth, "before-key", "next-key", 1))
			if _, err = m.Query(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
