package controller

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestSQLiteStorageCountsOnlyActiveFamiliesAndCompanions(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) uint64 {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, make([]byte, 8192), 0600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return uint64(info.Sys().(*syscall.Stat_t).Blocks) * 512
	}

	logs := write("history/logs.db") + write("history/logs.db-wal") + write("history/logs.db-shm")
	traffic := write("history/traffic.db")
	journal := write("history/journal/2026112118.db") + write("history/journal/2026112119.db") +
		write("history/journal/2026112119.db-wal")
	ai := write("ai_usage.db")
	profiles := write("AIProfiles/profile_events.db") + write("AIProfiles/profile_events.db-wal")
	write("history/journal/2026112119.db.corrupt-20261122T010000Z")
	write("history/logs.db.corrupt-20261122T010000Z")
	write("AIProfiles/other.db")

	got := readSQLiteStorage(dir, uint32(os.Geteuid()))
	if got == nil || got.LogsBytes != logs || got.TrafficBytes != traffic || got.JournalBytes != journal ||
		got.AiUsageBytes != ai || got.ProfileEventsBytes != profiles {
		t.Fatalf("unexpected storage usage: %v", got)
	}
}
