package aiprofile

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeLegacy(t *testing.T, root, relative string, v any) {
	t.Helper()
	dir := filepath.Dir(filepath.Join(root, relative))
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, relative), encodeState(v), 0600); e != nil {
		t.Fatal(e)
	}
}
func TestV1MigrationPreservesProfilesBindingsAndNativeFiles(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(home, "AIProfiles")
	old := filepath.Join(root, "v1")
	cli := filepath.Join(home, ".claude")
	if e := os.Mkdir(cli, 0700); e != nil {
		t.Fatal(e)
	}
	raw := []byte(`{"model":"native","env":{"ANTHROPIC_BASE_URL":"https://old.test","ANTHROPIC_AUTH_TOKEN":"old-key"}}`)
	if e := os.WriteFile(filepath.Join(cli, "settings.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	files := []nativeFile{{Name: "settings.json", Exists: true, Data: raw}}
	stripped, _, e := transform(Claude, files, urlSlot, keySlot)
	if e != nil {
		t.Fatal(e)
	}
	id := "4eb87612-9a70-49d3-88e0-a54d40245269"
	p := savedProfile{Schema: 1, Profile: Profile{ID: id, Revision: 3, ConnectionRevision: 2, Name: "Existing", BaseURL: "https://old.test", Multiplier: 0.3}, Key: "old-key"}
	writeLegacy(t, old, "profiles/"+id+".json", p)
	writeLegacy(t, old, "templates/claude.json", template{Schema: 1, Revision: 4, Files: stripped})
	writeLegacy(t, old, "original/claude.json", application{Schema: 1, Files: files})
	writeLegacy(t, old, "applied/claude.json", application{Schema: 1, ProfileID: id, ConnectionRevision: 2, TemplateRevision: 4, Files: files})
	m, e := New(Options{Directory: root, Home: home, Owner: uint32(os.Geteuid())})
	if e != nil {
		t.Fatal(e)
	}
	w, e := m.Query(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, p := range w.Profiles {
		if p.ID == id && p.Name == "Existing" && p.Revision == 3 {
			found = true
		}
	}
	if len(w.Profiles) != 1 {
		t.Fatal("migration duplicated the identical first-import backup")
	}
	if !found || w.Bindings[1].ProfileID != id || w.Bindings[1].State != "applied" {
		t.Fatal("migration lost original identity or binding")
	}
	b, _ := os.ReadFile(filepath.Join(cli, "settings.json"))
	if string(b) != string(raw) {
		t.Fatal("migration changed CLI files")
	}
	for _, name := range []string{"v1", "original", "applied", "pending.json"} {
		if _, e = os.Stat(filepath.Join(root, name)); !os.IsNotExist(e) {
			t.Fatal("legacy layout not removed:", name)
		}
	}
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	m, e = New(Options{Directory: root, Home: home, Owner: uint32(os.Geteuid())})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = m.Close() }()
	next, e := m.Query(context.Background())
	if e != nil || len(next.Profiles) != len(w.Profiles) {
		t.Fatal("restart duplicated migrated Profiles")
	}
}
func TestFlatLegacyMigrationMovesOriginalAccountIntoSelectableProfile(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(home, "state")
	dir := filepath.Join(home, ".dsh", "profiles", "desktop")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	patch := []byte("- name: '@deepseek-ai/dsh-agent-default-model'\n  config:\n    provider: deepseek-account\n    model: native-model\n")
	credentials := []byte("version: 1\nrefs: {}\nrecords:\n  account:\n    kind: grant\n    payload: preserved-account\n")
	files := []nativeFile{{Name: "profiles/desktop/cordis.patch.yml", Exists: true, Data: patch}, {Name: ".credentials.yaml", Exists: true, Data: credentials}}
	if e := os.WriteFile(filepath.Join(dir, "cordis.patch.yml"), patch, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(home, ".dsh", ".credentials.yaml"), credentials, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.MkdirAll(filepath.Join(root, "profiles"), 0700); e != nil {
		t.Fatal(e)
	}
	writeLegacy(t, root, "original/dsh.json", application{Schema: 1, Files: files})
	m, e := New(Options{Directory: root, Home: home, Owner: uint32(os.Geteuid()), DSHVersion: "0.2.1-rc.3"})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = m.Close() }()
	w, e := m.Query(context.Background())
	if e != nil || len(w.Profiles) != 1 {
		t.Fatal("account snapshot not migrated")
	}
	if w.Bindings[2].State != "applied" {
		t.Fatal("migrated current account was not marked applied")
	}
	p := w.Profiles[0]
	if p.Kind != "native" || p.NativeCLI != DSH || p.HasKey {
		t.Fatal("account was misrepresented as an API profile")
	}
	plan, e := m.Plan(context.Background(), PlanRequest{CLI: DSH, ProfileID: p.ID, Revision: p.Revision, InspectionID: w.Bindings[2].InspectionID})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Apply(context.Background(), ApplyRequest{PlanID: plan.ID, OperationID: "restore-account"}); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "cordis.patch.yml"))
	if string(b) != string(patch) {
		t.Fatal("account snapshot was not copied exactly")
	}
	if _, e = os.Stat(filepath.Join(root, "original")); !os.IsNotExist(e) {
		t.Fatal("standalone original directory remains")
	}
}

func TestMigrationInterruptedBeforeMetadataCommitResumesWithoutLosingSource(t *testing.T) {
	if root := os.Getenv("PAX_MIGRATION_CRASH_ROOT"); root != "" {
		_, e := New(Options{Directory: root, Home: filepath.Dir(root), Owner: uint32(os.Geteuid()), stateWriteHook: func(i int) error {
			if i == 2 {
				os.Exit(41)
			}
			return nil
		}})
		if e != nil {
			os.Exit(42)
		}
		os.Exit(43)
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(home, "AIProfiles")
	old := filepath.Join(root, "v1")
	id := "4eb87612-9a70-49d3-88e0-a54d40245269"
	files := []nativeFile{{Name: "settings.json", Exists: true, Data: []byte(`{"env":{"ANTHROPIC_BASE_URL":"https://before.test","ANTHROPIC_AUTH_TOKEN":"key"}}`)}}
	stripped, _, e := transform(Claude, files, urlSlot, keySlot)
	if e != nil {
		t.Fatal(e)
	}
	writeLegacy(t, old, "profiles/"+id+".json", savedProfile{Schema: 1, Profile: Profile{ID: id, Revision: 1, ConnectionRevision: 1, Name: "Saved", BaseURL: "https://before.test", Multiplier: 1}, Key: "key"})
	writeLegacy(t, old, "templates/claude.json", template{Schema: 1, Revision: 1, Files: stripped})
	cmd := exec.Command(os.Args[0], "-test.run=^TestMigrationInterruptedBeforeMetadataCommitResumesWithoutLosingSource$")
	cmd.Env = append(os.Environ(), "PAX_MIGRATION_CRASH_ROOT="+root)
	if e = cmd.Run(); e == nil {
		t.Fatal("migration was not interrupted")
	}
	if _, e = os.Stat(filepath.Join(old, "profiles", id+".json")); e != nil {
		t.Fatal("source deleted before successful migration")
	}
	m, e := New(Options{Directory: root, Home: home, Owner: uint32(os.Geteuid())})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = m.Close() }()
	w, e := m.Query(context.Background())
	if e != nil || len(w.Profiles) != 1 || w.Profiles[0].ID != id {
		t.Fatal("migration could not recover after interruption")
	}
	if _, e = os.Stat(filepath.Join(root, "profiles", id, "claude", "settings.json")); e != nil {
		t.Fatal("generated configuration lost")
	}
}
