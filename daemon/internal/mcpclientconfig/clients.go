package mcpclientconfig

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Client contains presentation facts only; no configuration or credentials.
type Client struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	State       string `json:"state"`
	EntryName   string `json:"entryName"`
	Permissions string `json:"permissions"`
}

// Manager edits external MCP fields. Roots are resolved by the composition layer.
type Manager struct{ Home, Launcher, CodexRoot, ClaudeRoot, DSHRoot string }
type target struct {
	id, name, path, format string
	inherited              string
	available              bool
}

// New uses the caller's effective environment, matching client configuration roots.
func New(home, launcher string) *Manager {
	root := func(env, fallback string) string {
		if v := os.Getenv(env); filepath.IsAbs(v) {
			return v
		}
		return filepath.Join(home, fallback)
	}
	claude := filepath.Join(home, ".claude.json")
	if v := os.Getenv("CLAUDE_CONFIG_DIR"); filepath.IsAbs(v) {
		claude = filepath.Join(v, ".claude.json")
	}
	return &Manager{Home: home, Launcher: launcher, CodexRoot: root("CODEX_HOME", ".codex"), ClaudeRoot: claude, DSHRoot: root("DSH_HOME", ".dsh")}
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }
func (m *Manager) targets() []target {
	out := []target{
		{id: "codex", name: "Codex", path: filepath.Join(m.CodexRoot, "config.toml"), format: "toml", available: exists(m.CodexRoot)},
		{id: "claude-code", name: "Claude Code", path: m.ClaudeRoot, format: "json", available: exists(m.ClaudeRoot) || exists(filepath.Join(m.Home, ".claude"))},
		{id: "dsh-desktop", name: "DSH 桌面", path: filepath.Join(m.DSHRoot, "profiles/desktop/cordis.patch.yml"), format: "yaml", inherited: filepath.Join(m.DSHRoot, "cordis.patch.yml"), available: exists(filepath.Join(m.DSHRoot, "profiles/desktop"))},
	}
	profiles, _ := os.ReadDir(filepath.Join(m.DSHRoot, "profiles"))
	for _, p := range profiles {
		if !p.IsDir() || p.Name() == "desktop" || p.Name() == "web" {
			continue
		}
		profile := filepath.Join(m.DSHRoot, "profiles", p.Name())
		if !exists(filepath.Join(profile, "cordis.yml")) && !exists(filepath.Join(profile, "cordis.patch.yml")) {
			continue
		}
		out = append(out, target{id: "dsh-cli:" + p.Name(), name: "DSH CLI · " + p.Name(), path: filepath.Join(m.DSHRoot, "profiles", p.Name(), "cordis.patch.yml"), format: "yaml", inherited: filepath.Join(m.DSHRoot, "cordis.patch.yml"), available: true})
	}
	return out
}

// Inspect performs read-only inspection; absence and errInvalid files are distinct.
func (m *Manager) Inspect() []Client {
	out := []Client{}
	for _, t := range m.targets() {
		out = append(out, m.inspect(t))
	}
	return out
}
func (m *Manager) inspect(t target) Client {
	file, err := readFile(t.path)
	return m.inspectFile(t, file, err)
}

func (m *Manager) inspectFile(t target, file observation, readError error) Client {
	c := Client{ID: t.id, Name: t.name, State: "missing"}
	if !t.available {
		c.State = "not_found"
		return c
	}
	err := readError
	if err != nil {
		c.State = "unreadable"
		return c
	}
	d, err := parse(file.data, t.format)
	if err != nil {
		c.State = "invalid"
		return c
	}
	entries, err := d.entries()
	if err != nil {
		c.State = "conflict"
		return c
	}
	if t.inherited != "" {
		f, e := readFile(t.inherited)
		if e != nil {
			c.State = "unreadable"
			return c
		}
		i, e := parse(f.data, "yaml")
		if e != nil {
			c.State = "invalid"
			return c
		}
		parents, e := i.entries()
		if e != nil {
			c.State = "conflict"
			return c
		}
		if len(parents) > 0 {
			c.State = "inherited"
			return c
		}
	}
	if len(entries) > 1 {
		c.State = "conflict"
		return c
	}
	if len(entries) == 0 {
		return c
	}
	e := entries[0]
	c.EntryName = e.name
	args, ok := arguments(e.config["args"])
	if !ok {
		c.State = "conflict"
		return c
	}
	permissions, valid := parseArgs(args)
	if !valid || !paxCommand(e.config["command"], m.Launcher) {
		c.State = "conflict"
		return c
	}
	c.Permissions = permissions
	c.State = "configured"
	if e.config["command"] != m.Launcher {
		c.State = "outdated"
	}
	if e.config["enabled"] == false || e.disabled {
		c.State = "disabled"
	}
	if value, ok := e.config["enabled"]; ok {
		if _, valid := value.(bool); !valid {
			c.State = "conflict"
		}
	}
	if transport, ok := e.config["type"]; ok && transport != "stdio" {
		c.State = "conflict"
	}
	if transport, ok := e.config["transport"]; ok && transport != "stdio" {
		c.State = "conflict"
	}
	for _, key := range []string{"url", "env", "env_vars", "cwd"} {
		if _, ok := e.config[key]; ok {
			c.State = "conflict"
		}
	}
	return c
}

func paxCommand(value any, launcher string) bool {
	s, ok := value.(string)
	return ok && (s == launcher || filepath.Base(s) == "pax-mcp" && (strings.Contains(s, "Pax Connect/") || strings.Contains(s, "PaxConnect.app/Contents/MacOS/")))
}
func arguments(v any) ([]string, bool) {
	if v == nil {
		return nil, true
	}
	a, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := []string{}
	for _, v := range a {
		s, ok := v.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}
func parseArgs(args []string) (string, bool) {
	allow := "read"
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		key := args[i]
		if key != "--allow" && key != "--client-id" || seen[key] || i+1 == len(args) {
			return "", false
		}
		seen[key] = true
		i++
		if key == "--allow" {
			allow = args[i]
		} else if len(args[i]) == 0 || len(args[i]) > 128 || strings.ContainsAny(args[i], "/\\\x00\r\n") {
			return "", false
		}
	}
	valid := map[string]bool{"read": true, "manage": true, "network": true, "diagnose": true, "profiles": true, "settings": true, "delete": true}
	set := map[string]bool{}
	for _, p := range strings.Split(allow, ",") {
		if !valid[p] {
			return "", false
		}
		set[p] = true
	}
	out := []string{}
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return strings.Join(out, ","), true
}

// Connect observes again at action time. Correct registrations are never rewritten.
// expectedState prevents an install click silently becoming an update after a race.
func (m *Manager) Connect(ctx context.Context, id, allow, expectedState string) error {
	if allow != "read" && allow != "read,diagnose" {
		return errors.New("invalid_permission")
	}
	for _, t := range m.targets() {
		if t.id != id {
			continue
		}
		f, err := readFile(t.path)
		c := m.inspectFile(t, f, err)
		if c.State == "configured" {
			return nil
		}
		if c.State != expectedState {
			return errors.New("configuration_changed")
		}
		if c.State != "missing" && c.State != "outdated" && c.State != "disabled" {
			return errors.New("configuration_conflict")
		}
		if err != nil {
			return err
		}
		d, err := parse(f.data, t.format)
		if err != nil {
			return err
		}
		if c.Permissions != "" {
			allow = c.Permissions
		}
		if err = d.merge(m.Launcher, id, allow); err != nil {
			return err
		}
		data, err := d.encode()
		if err != nil {
			return err
		}
		if err = replaceFile(ctx, m.Home, f, data); err != nil {
			return err
		}
		if after := m.inspect(t); after.State != "configured" {
			return errors.New("configuration_verify_failed")
		}
		return nil
	}
	return errors.New("client_not_found")
}
