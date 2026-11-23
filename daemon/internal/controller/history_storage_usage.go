package controller

import (
	"path/filepath"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/historystore"
)

const historyStorageSampleInterval = 5 * time.Second

// readSQLiteStorage samples metadata only. A failed item leaves the total unknown
// so the App never displays a partial sum as the full footprint.
func readSQLiteStorage(directory string, owner uint32) *pb.SQLiteStorageUsage {
	history := filepath.Join(directory, "history")
	usage := &pb.SQLiteStorageUsage{}
	for _, file := range []struct {
		path  string
		bytes *uint64
	}{
		{filepath.Join(history, "logs.db"), &usage.LogsBytes},
		{filepath.Join(history, "traffic.db"), &usage.TrafficBytes},
		{filepath.Join(directory, "ai_usage.db"), &usage.AiUsageBytes},
		{filepath.Join(directory, "AIProfiles", "profile_events.db"), &usage.ProfileEventsBytes},
	} {
		size, err := historystore.SQLiteDiskBytes(file.path, owner)
		if err != nil {
			return nil
		}
		*file.bytes = size
	}
	bytes, err := historystore.JournalDiskBytes(history, owner)
	if err != nil {
		return nil
	}
	usage.JournalBytes = bytes
	return usage
}
