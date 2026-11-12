package core

import (
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"
)

type directoryInfo struct {
	mode     os.FileMode
	uid, gid uint32
}

func (i directoryInfo) Name() string       { return "directory" }
func (i directoryInfo) Size() int64        { return 0 }
func (i directoryInfo) Mode() os.FileMode  { return i.mode }
func (i directoryInfo) ModTime() time.Time { return time.Time{} }
func (i directoryInfo) IsDir() bool        { return i.mode.IsDir() }
func (i directoryInfo) Sys() any           { return &syscall.Stat_t{Uid: i.uid, Gid: i.gid} }

func directoryFixture() (map[string]directoryInfo, directoryHost, *[]string) {
	entries := map[string]directoryInfo{}
	for _, p := range []string{"/", "/private", "/private/var"} {
		entries[p] = directoryInfo{mode: os.ModeDir | 0755}
	}
	entries["/private/var/run"] = directoryInfo{mode: os.ModeDir | 0775, gid: 1}
	var changes []string
	h := directoryHost{
		lstat: func(p string) (os.FileInfo, error) {
			v, ok := entries[p]
			if !ok {
				return nil, os.ErrNotExist
			}
			return v, nil
		},
		mkdir: func(p string, m os.FileMode) error {
			if _, ok := entries[p]; ok {
				return os.ErrExist
			}
			entries[p] = directoryInfo{mode: os.ModeDir | m, uid: 501, gid: 20}
			changes = append(changes, "mkdir "+p)
			return nil
		},
		chown: func(p string, uid, gid int) error {
			v := entries[p]
			v.uid = uint32(uid)
			v.gid = uint32(gid)
			entries[p] = v
			changes = append(changes, "chown "+p)
			return nil
		},
		chmod: func(p string, m os.FileMode) error {
			v := entries[p]
			v.mode = os.ModeDir | m
			entries[p] = v
			changes = append(changes, "chmod "+p)
			return nil
		},
	}
	return entries, h, &changes
}
func TestDocumentedDirectoryPathsNonRoot(t *testing.T) {
	entries, h, changes := directoryFixture()
	socketParent := "/private/var/run/pax-core"
	state := filepath.Join(socketParent, "state")
	for _, v := range []struct {
		path string
		mode os.FileMode
	}{{state, 0700}, {socketParent, 0755}, {filepath.Join(state, "rulesets"), 0700}} {
		if err := protectDirectory(v.path, v.mode, h); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"mkdir " + socketParent, "chown " + socketParent, "chmod " + socketParent, "mkdir " + state, "chown " + state, "chmod " + state, "mkdir " + state + "/rulesets", "chown " + state + "/rulesets", "chmod " + state + "/rulesets"}
	if !reflect.DeepEqual(*changes, want) {
		t.Fatal(*changes)
	}
	if entries["/private/var/run"].gid != 1 || entries["/private/var/run"].mode != os.ModeDir|0775 {
		t.Fatal("system parent changed")
	}
	// Existing valid directories cause no mkdir/chown/chmod at all.
	*changes = nil
	if err := protectDirectory(state, 0700, h); err != nil {
		t.Fatal(err)
	}
	if len(*changes) != 0 {
		t.Fatal("modified existing directories")
	}
}
func TestUnsafeDirectoriesRefusedWithoutRepair(t *testing.T) {
	for _, tt := range []struct {
		name, path, mutate string
		info               directoryInfo
	}{
		{"existing_mode", "/private/var/run/pax-core", "/private/var/run/pax-core", directoryInfo{mode: os.ModeDir | 0775}},
		{"existing_owner", "/private/var/run/pax-core", "/private/var/run/pax-core", directoryInfo{mode: os.ModeDir | 0755, uid: 501}},
		{"existing_group", "/private/var/run/pax-core", "/private/var/run/pax-core", directoryInfo{mode: os.ModeDir | 0755, gid: 1}},
		{"existing_symlink", "/private/var/run/pax-core", "/private/var/run/pax-core", directoryInfo{mode: os.ModeSymlink | 0755}},
		{"distant_writable", "/private/var/run/pax-core", "/private", directoryInfo{mode: os.ModeDir | 0775}},
		{"sticky_not_exception", "/private/var/run/pax-core", "/private", directoryInfo{mode: os.ModeDir | os.ModeSticky | 01777}},
		{"wrong_trusted_uid", "/private/var/run/pax-core", "/private/var/run", directoryInfo{mode: os.ModeDir | 0775, uid: 501, gid: 1}},
		{"wrong_trusted_gid", "/private/var/run/pax-core", "/private/var/run", directoryInfo{mode: os.ModeDir | 0775}},
		{"wrong_trusted_mode", "/private/var/run/pax-core", "/private/var/run", directoryInfo{mode: os.ModeDir | 0777, gid: 1}},
		{"system_as_target", "/private/var/run", "/private/var/run", directoryInfo{mode: os.ModeDir | 0775, gid: 1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			entries, h, changes := directoryFixture()
			entries[tt.mutate] = tt.info
			err := protectDirectory(tt.path, 0755, h)
			if err == nil {
				t.Fatal("unsafe directory accepted")
			}
			if len(*changes) != 0 {
				t.Fatal("repaired existing path", *changes)
			}
		})
	}
}
