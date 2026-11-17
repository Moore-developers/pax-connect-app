package mcpclientconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func fixture(t *testing.T) *Manager {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{Home: home, Launcher: filepath.Join(home, "Library/Application Support/Pax Connect/bin/pax-mcp"), CodexRoot: filepath.Join(home, ".codex"), ClaudeRoot: filepath.Join(home, ".claude.json"), DSHRoot: filepath.Join(home, ".dsh")}
	for _, dir := range []string{m.CodexRoot, filepath.Join(home, ".claude"), filepath.Join(m.DSHRoot, "profiles/desktop"), filepath.Join(m.DSHRoot, "profiles/headless")} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(m.DSHRoot, "profiles/headless/cordis.yml"), "[]\n")
	return m
}
func write(t *testing.T, path, raw string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
}
func client(t *testing.T, m *Manager, id string) Client {
	t.Helper()
	for _, c := range m.Inspect() {
		if c.ID == id {
			return c
		}
	}
	t.Fatal("missing client", id)
	return Client{}
}

func TestConnectPreservesOtherClientsAndRepeatDoesNotWrite(t *testing.T) {
	m := fixture(t)
	inputs := map[string]string{
		"toml": "model = 'gpt-6.1-sol'\n[mcp_servers.docs]\ncommand = 'docs'\nargs = ['--token', 'not-to-be-returned']\n",
		"json": `{"large":9007199254740993,"mcpServers":{"docs":{"command":"docs","args":["secret"]}},"projects":{"p":{"allowedTools":["other"]}}}`,
		"yaml": "# keep plugin comment\n- insert:\n    - id: docs\n      name: '@example/docs'\n      config:\n        secret: keep-me\n",
	}
	for _, target := range m.targets() {
		t.Run(target.id, func(t *testing.T) {
			write(t, target.path, inputs[target.format])
			before, err := parse([]byte(inputs[target.format]), target.format)
			if err != nil {
				t.Fatal(err)
			}
			if err = m.Connect(context.Background(), target.id, "read,diagnose", "missing"); err != nil {
				t.Fatal(err)
			}
			state := client(t, m, target.id)
			if state.State != "configured" || state.Permissions != "diagnose,read" {
				t.Fatal(state)
			}
			f, err := readFile(target.path)
			if err != nil {
				t.Fatal(err)
			}
			after, err := parse(f.data, target.format)
			if err != nil {
				t.Fatal(err)
			}
			if target.format != "yaml" {
				key := "mcp_servers"
				if target.format == "json" {
					key = "mcpServers"
				}
				delete(after.object[key].(map[string]any), "pax")
				if !reflect.DeepEqual(before.object, after.object) {
					t.Fatal("non-Pax configuration changed")
				}
			} else if !bytes.Contains(f.data, []byte("# keep plugin comment")) || !bytes.Contains(f.data, []byte("secret: keep-me")) {
				t.Fatal("existing plugin/comment lost")
			}
			if err = m.Connect(context.Background(), target.id, "read", "missing"); err != nil {
				t.Fatal(err)
			}
			second, err := readFile(target.path)
			if err != nil || !sameFile(f, second) {
				t.Fatal("repeat rewrote configuration", err)
			}
			if state = client(t, m, target.id); state.Permissions != "diagnose,read" {
				t.Fatal("repeat changed permissions", state)
			}
		})
	}
	backups, err := os.ReadDir(filepath.Join(m.Home, "Library/Application Support/Pax Connect/mcp-backups"))
	if err != nil || len(backups) != 4 {
		t.Fatal("missing or duplicate backups", len(backups), err)
	}
}

func TestAliasUpdateAndDisabledRetainPermissions(t *testing.T) {
	m := fixture(t)
	path := filepath.Join(m.CodexRoot, "config.toml")
	write(t, path, "[mcp_servers.my_pax]\ncommand='/Applications/PaxConnect.app/Contents/MacOS/pax-mcp'\nargs=['--client-id','existing','--allow','read,manage,delete']\nenabled=false\n")
	if c := client(t, m, "codex"); c.State != "disabled" || c.EntryName != "my_pax" {
		t.Fatal(c)
	}
	if err := m.Connect(context.Background(), "codex", "read", "disabled"); err != nil {
		t.Fatal(err)
	}
	state := client(t, m, "codex")
	if state.State != "configured" || state.EntryName != "my_pax" || state.Permissions != "delete,manage,read" {
		t.Fatal(state)
	}
	f, _ := readFile(path)
	d, _ := parse(f.data, "toml")
	if len(d.object["mcp_servers"].(map[string]any)) != 1 {
		t.Fatal("duplicate entry")
	}
}

func TestConflictsAndInvalidDocumentsNeverWrite(t *testing.T) {
	m := fixture(t)
	path := filepath.Join(m.CodexRoot, "config.toml")
	for _, raw := range []string{
		"[mcp_servers.pax]\ncommand='unrelated'\n",
		"[mcp_servers.pax]\ncommand='" + m.Launcher + "'\nargs=['--allow','read','--socket','/outside']\n",
		"[mcp_servers.pax]\ncommand='" + m.Launcher + "'\n[mcp_servers.alias]\ncommand='" + m.Launcher + "'\n",
		"[mcp_servers.pax]\ncommand='" + m.Launcher + "'\nenv={HOME='/outside'}\n",
		"not valid TOML",
	} {
		write(t, path, raw)
		state := client(t, m, "codex")
		if state.State != "conflict" && state.State != "invalid" {
			t.Fatal(state)
		}
		if err := m.Connect(context.Background(), "codex", "read", state.State); err == nil {
			t.Fatal("conflict overwritten")
		}
		after, _ := os.ReadFile(path)
		if string(after) != raw {
			t.Fatal("rejection changed file")
		}
	}
	for format, raw := range map[string]string{"json": `{"mcpServers":{},"mcpServers":{}}`, "yaml": "- insert: []\n  insert: []\n"} {
		if _, err := parse([]byte(raw), format); err == nil {
			t.Fatal("duplicate keys accepted", format)
		}
	}
}

func TestExternalReplacementAndSymlinkAreRejected(t *testing.T) {
	m := fixture(t)
	path := filepath.Join(m.CodexRoot, "config.toml")
	write(t, path, "model='before'\n")
	before, err := readFile(path)
	if err != nil {
		t.Fatal(err)
	}
	write(t, path, "model='external'\n")
	if err = replaceFile(context.Background(), m.Home, before, []byte("model='ours'\n")); err == nil || err.Error() != "configuration_changed" {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "model='external'\n" {
		t.Fatal("external edit lost")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(m.ClaudeRoot, path); err != nil {
		t.Fatal(err)
	}
	if _, err = readFile(path); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestDSHInheritedRegistrationIsNotDuplicated(t *testing.T) {
	m := fixture(t)
	global := filepath.Join(m.DSHRoot, "cordis.patch.yml")
	raw := "- insert:\n    - id: inherited\n      name: '@deepseek-ai/dsh-mcp-client'\n      config:\n        serverName: pax\n        transport: stdio\n        command: '" + m.Launcher + "'\n"
	write(t, global, raw)
	for _, id := range []string{"dsh-desktop", "dsh-cli:headless"} {
		if c := client(t, m, id); c.State != "inherited" {
			t.Fatal(c)
		}
		if e := m.Connect(context.Background(), id, "read", "inherited"); e == nil {
			t.Fatal("duplicated inherited config")
		}
	}
}

func TestMissingFileConnectAndReportDoesNotExposeSecrets(t *testing.T) {
	m := fixture(t)
	if err := m.Connect(context.Background(), "claude-code", "read", "missing"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(m.ClaudeRoot)
	var d map[string]any
	if json.Unmarshal(b, &d) != nil {
		t.Fatal("errInvalid persisted JSON")
	}
	if client(t, m, "claude-code").State != "configured" {
		t.Fatal("not configured")
	}
	write(t, filepath.Join(m.CodexRoot, "config.toml"), "secret='PRIVATE-SECRET'\n")
	report, _ := json.Marshal(m.Inspect())
	if bytes.Contains(report, []byte("PRIVATE-SECRET")) {
		t.Fatal("report leaked configuration")
	}
}

func TestDSHOnlyListsActualCLIProfiles(t *testing.T) {
	m := fixture(t)
	for _, name := range []string{"node_modules", "web"} {
		if err := os.MkdirAll(filepath.Join(m.DSHRoot, "profiles", name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range m.Inspect() {
		if c.ID == "dsh-cli:node_modules" || c.ID == "dsh-cli:web" {
			t.Fatal("non-CLI directory exposed", c)
		}
	}
}
