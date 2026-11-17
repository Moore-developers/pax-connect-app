package mcpinstall

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The signed real-bundle matrix exercises Verify; these tests cover trust
// boundaries that must reject before invoking an external executable.
func TestRegistrationRejectsUnsafeInputs(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	dir, _ := Directory()
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "mcp-install.json")
	cases := []struct {
		name  string
		setup func()
	}{
		{"missing", func() {}},
		{"bad_json", func() { mustWrite(t, path, []byte("{"), 0600) }},
		{"public_record", func() { mustWrite(t, path, []byte(`{"bundle_path":"/Applications/PaxConnect.app"}`), 0644) }},
		{"relative_bundle", func() { mustWrite(t, path, []byte(`{"bundle_path":"relative.app"}`), 0600) }},
		{"symlink", func() {
			if e := os.Symlink("/etc/passwd", path); e != nil {
				t.Fatal(e)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Remove(path)
			tc.setup()
			if _, e := Resolve(context.Background()); e == nil {
				t.Fatal("unsafe registration accepted")
			}
		})
	}
}

func TestRefreshDoesNotInstallOnFirstLaunch(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	if target, e := Install(context.Background(), "/missing.app", true); e != nil || target != "" {
		t.Fatal(target, e)
	}
	if _, e := os.Stat(filepath.Join(home, "Library")); !os.IsNotExist(e) {
		t.Fatal("refresh created state", e)
	}
}

func TestRegistrationDoesNotTrustSelfClaimedHash(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	dir, _ := Directory()
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	fake := filepath.Join(home, "Fake.app")
	if e := os.Mkdir(fake, 0700); e != nil {
		t.Fatal(e)
	}
	data, _ := json.Marshal(Bundle{Path: fake, CodeHash: "self-claimed", Fingerprint: "self-claimed", Build: "1"})
	if e := record(dir).Write(context.Background(), data); e != nil {
		t.Fatal(e)
	}
	if _, e := Resolve(context.Background()); e == nil {
		t.Fatal("unsigned fake bundle accepted")
	}
}

func TestLauncherRejectsMismatchAndUnsafeMode(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	bundle := Bundle{Path: filepath.Join(home, "App.app")}
	binary := filepath.Join(bundle.Path, "Contents/MacOS/pax-mcp-launcher")
	if e := os.MkdirAll(filepath.Dir(binary), 0700); e != nil {
		t.Fatal(e)
	}
	mustWrite(t, binary, []byte("correct build"), 0700)
	installed := filepath.Join(home, "pax-mcp")
	mustWrite(t, installed, []byte("correct build"), 0700)
	if e := bundle.CheckLauncher(installed); e != nil {
		t.Fatal(e)
	}
	mustWrite(t, installed, []byte("another build"), 0700)
	if e := bundle.CheckLauncher(installed); e == nil {
		t.Fatal("mismatched launcher accepted")
	}
	mustWrite(t, installed, []byte("correct build"), 0755)
	if e := bundle.CheckLauncher(installed); e == nil {
		t.Fatal("public launcher accepted")
	}
	if e := os.Remove(installed); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(binary, installed); e != nil {
		t.Fatal(e)
	}
	if e := bundle.CheckLauncher(installed); e == nil {
		t.Fatal("symlink accepted")
	}
}

func mustWrite(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if e := os.WriteFile(path, data, mode); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(path, mode); e != nil {
		t.Fatal(e)
	}
}
