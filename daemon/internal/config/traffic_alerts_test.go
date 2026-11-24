package config

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestTrafficAlertSettingsAndEpisodePersistence(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store := NewTrafficAlertStore(dir, uint32(os.Geteuid()))
	p, err := store.LoadSettings()
	if err != nil || p.Enabled {
		t.Fatal("default must be off", err)
	}
	p.Enabled = true
	p.MediumSeconds = 7200
	p.Short.Upload = 1
	p.Revision++
	if err = store.SaveSettings(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	n := TrafficAlertNotice{ID: "id", Key: "app:x:u", Direction: "upload", WindowSeconds: 300, Threshold: 1, Until: time.Now().UTC().Truncate(time.Second)}
	runtime := TrafficAlertRuntime{Revision: p.Revision, Episodes: map[string]TrafficAlertEpisode{n.Key: {Latest: n}}}
	if err = store.SaveRuntime(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	reopened := NewTrafficAlertStore(dir, uint32(os.Geteuid()))
	got, err := reopened.LoadSettings()
	if err != nil || got != p {
		t.Fatal(got, err)
	}
	state, err := reopened.LoadRuntime()
	if err != nil || !reflect.DeepEqual(state, runtime) {
		t.Fatal(state, err)
	}
	for _, name := range []string{"traffic-alert-settings.json", "traffic-alert-state.json"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("private mode", info, err)
		}
	}
	p.Short.Upload = 0
	if store.SaveSettings(context.Background(), p) == nil {
		t.Fatal("accepted zero")
	}
}
