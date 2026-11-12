package core

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPinnedRulesCopy(t *testing.T) {
	source := filepath.Join("..", "..", "..", "rulesets")
	dest := t.TempDir()
	if err := copyPinnedRules(source, dest, bundledRules); err != nil {
		t.Fatal(err)
	}
	for name, hash := range bundledRules {
		p := filepath.Join(dest, name)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != hash {
			t.Fatal("hash mismatch")
		}
		info, err := os.Stat(p)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("unsafe copy mode")
		}
	}
}
func TestRulesSourceAttacksAndIsolation(t *testing.T) {
	for _, kind := range []string{"symlink", "directory", "fifo", "altered", "mutated_after_copy"} {
		t.Run(kind, func(t *testing.T) {
			source, dest := t.TempDir(), t.TempDir()
			name := "geosite-cn.srs"
			p := filepath.Join(source, name)
			content := []byte("pinned fixture")
			expected := map[string]string{name: fmt.Sprintf("%x", sha256.Sum256(content))}
			var err error
			switch kind {
			case "symlink":
				target := filepath.Join(t.TempDir(), "target")
				if err = os.WriteFile(target, content, 0600); err == nil {
					err = os.Symlink(target, p)
				}
			case "directory":
				err = os.Mkdir(p, 0700)
			case "fifo":
				err = unix.Mkfifo(p, 0600)
			case "altered":
				err = os.WriteFile(p, []byte("altered"), 0600)
			default:
				err = os.WriteFile(p, content, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			// A symlink at the destination is replaced atomically, never followed.
			outside := filepath.Join(t.TempDir(), "outside")
			if err = os.WriteFile(outside, []byte("untouched"), 0600); err != nil {
				t.Fatal(err)
			}
			if err = os.Symlink(outside, filepath.Join(dest, name)); err != nil {
				t.Fatal(err)
			}
			err = copyPinnedRules(source, dest, expected)
			if kind == "mutated_after_copy" {
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(p, []byte("later user modification"), 0600); err != nil {
					t.Fatal(err)
				}
				copyData, e := os.ReadFile(filepath.Join(dest, name))
				if e != nil || string(copyData) != string(content) {
					t.Fatal("copy changed with source")
				}
			} else if err == nil {
				t.Fatal("unsafe source copied")
			}
			outsideData, e := os.ReadFile(outside)
			if e != nil || string(outsideData) != "untouched" {
				t.Fatal("followed destination symlink")
			}
		})
	}
}
