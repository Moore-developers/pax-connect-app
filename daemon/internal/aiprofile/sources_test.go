package aiprofile

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAccountDiscoveryNeedsNoProfileAndNeverWritesCLIFiles(t *testing.T) {
	for _, cli := range []CLI{Claude, Codex} {
		t.Run(string(cli), func(t *testing.T) {
			home, _ := filepath.EvalSymlinks(t.TempDir())
			name := ".claude"
			files := map[string]string{".claude.json": `{"oauthAccount":{"emailAddress":"fixture@example.test"}}`, ".claude/settings.json": `{"permissions":{"allow":["Read"]}}`}
			if cli == Codex {
				name = ".codex"
				files = map[string]string{".codex/config.toml": "model = 'keep-model'\n", ".codex/auth.json": `{"auth_mode":"chatgpt","tokens":{"access_token":"fixture-account-secret"}}`}
			}
			if err := os.Mkdir(filepath.Join(home, name), 0700); err != nil {
				t.Fatal(err)
			}
			for path, raw := range files {
				if err := os.WriteFile(filepath.Join(home, path), []byte(raw), 0600); err != nil {
					t.Fatal(err)
				}
			}
			m, err := New(Options{Home: home, Directory: filepath.Join(home, "state"), Owner: uint32(os.Geteuid())})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = m.Close() }()
			for range 3 {
				w, err := m.Query(context.Background())
				if err != nil || len(w.Profiles) != 0 {
					t.Fatal("account discovery required a Profile", err)
				}
				for _, b := range w.Bindings {
					if b.CLI == cli && (b.State != "account" || b.ServiceSource != "official_account" || b.ProfileID != "") {
						t.Fatalf("account projection %+v", b)
					}
				}
				raw, _ := json.Marshal(w)
				if strings.Contains(string(raw), "fixture-account-secret") {
					t.Fatal("account token escaped projection")
				}
			}
			var n int
			if err := m.events.QueryRow("SELECT count(*) FROM profile_events WHERE cli=? AND kind='observe'", cli).Scan(&n); err != nil || n != 1 {
				t.Fatalf("repeated inspection produced %d events: %v", n, err)
			}
			for path, want := range files {
				got, _ := os.ReadFile(filepath.Join(home, path))
				if string(got) != want {
					t.Fatal("changed account-owned file")
				}
			}
		})
	}
}

func TestReturningToAccountStopsOldProfileAndSurvivesRestart(t *testing.T) {
	m, home := claudeFixture(t)
	w := importClaude(t, m)
	p := w.Profiles[0]
	ctx := context.Background()
	plan, err := m.Plan(ctx, PlanRequest{CLI: Claude, ProfileID: p.ID, Revision: p.Revision, InspectionID: w.Bindings[1].InspectionID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(ctx, ApplyRequest{PlanID: plan.ID, OperationID: "source-apply"}); err != nil {
		t.Fatal(err)
	}
	apiAt := time.Now()
	if err = os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"permissions":{"allow":["Read"]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"oauthAccount":{"emailAddress":"fixture@example.test"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	b := bindingFor(t, m, Claude)
	if b.ProfileID != "" || b.ServiceSource != "official_account" {
		t.Fatalf("stale binding %+v", b)
	}
	accountAt := time.Now()
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = New(Options{Home: home, Directory: filepath.Join(home, "state"), Owner: uint32(os.Geteuid())})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	tl, err := m.Timeline(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if a := tl.Resolve(Claude, accountAt); a.ProfileID != "" || a.Multiplier != 1 || a.ServiceSource != "official_account" {
		t.Fatalf("account inherited API ownership %+v", a)
	}
	if a := tl.Resolve(Claude, apiAt); a.ProfileID != p.ID || a.ServiceSource != "relay_api" {
		t.Fatalf("late API request lost history %+v", a)
	}
}

func TestOfficialDefaultAPIToRelayPreservesUnrelatedSettings(t *testing.T) {
	for _, cli := range []CLI{Claude, Codex} {
		t.Run(string(cli), func(t *testing.T) {
			files := []nativeFile{{Name: "settings.json", Exists: true, Data: []byte(`{"env":{"ANTHROPIC_API_KEY":"old","KEEP" : "value"},"permissions" : {"allow":["Read"]}}`)}}
			if cli == Codex {
				files = []nativeFile{{Name: "config.toml", Exists: true, Data: []byte("# keep this\nmodel = 'keep-model'\n[mcp_servers.keep]\ncommand = 'keep-command'\n")}, {Name: "auth.json", Exists: true, Data: []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"old","KEEP" : "value"}`)}}
			}
			_, old, err := transform(cli, files, "", "")
			if err != nil || sourceOfEndpoint(cli, old.url) != "official_api" {
				t.Fatal("default official API not recognized", err)
			}
			next, err := patchCurrent(cli, files, "https://relay.example/v1", "next-key")
			if err != nil {
				t.Fatal(err)
			}
			_, v, err := transform(cli, next, "", "")
			if err != nil || v.url != "https://relay.example/v1" || v.key != "next-key" {
				t.Fatal("API switch not effective", err)
			}
			if cli == Codex && !strings.Contains(string(fileData(next, "config.toml")), string(fileData(files, "config.toml"))) {
				t.Fatal("lost model/MCP/comment bytes")
			}
			if cli == Claude && !strings.Contains(string(fileData(next, "settings.json")), `"permissions" : {"allow":["Read"]}`) {
				t.Fatal("lost permission bytes")
			}
		})
	}
}

func TestOfficialHostDoesNotMatchLookalikes(t *testing.T) {
	for _, raw := range []string{"https://api.openai.com.evil.test/v1", "http://api.openai.com/v1", "https://api.openai.com:8443/v1", "https://api.openai.com/gateway"} {
		if sourceOfEndpoint(Codex, raw) == "official_api" {
			t.Fatal("claimed official route", raw)
		}
	}
}

func TestOfficialAPIKeyReplacementDoesNotCreateAnEmptyConfig(t *testing.T) {
	files := []nativeFile{{Name: "config.toml"}, {Name: "auth.json", Exists: true, Data: []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"old"}`)}}
	next, err := patchCurrent(Codex, files, "https://api.openai.com/v1", "new-key")
	if err != nil || next[0].Exists {
		t.Fatal("replacing an API key created config.toml", err)
	}
	_, route, err := transform(Codex, next, "", "")
	if err != nil || route.key != "new-key" {
		t.Fatal("API key was not replaced", err)
	}
}
