package controller

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

func TestHourlyRetentionPrunesAndSurvivesRestart(t *testing.T) {
	for _, hours := range []uint32{1, 12, 24} {
		t.Run(fmt.Sprint(hours), func(t *testing.T) {
			ctx := context.Background()
			dir := newLogsTestDir(t)
			c, _ := newLogsTestController(t, dir)
			var err error
			now := time.Now()
			for _, age := range []time.Duration{30 * time.Minute, time.Duration(hours)*time.Hour + time.Minute} {
				if err = c.RecordEvent(ctx, config.HistoryEvent{ID: age.String(), At: now.Add(-age), Kind: "operation", Title: "fixture", Level: "info"}); err != nil {
					t.Fatal(err)
				}
			}
			snapshot, err := c.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
			if err != nil {
				t.Fatal(err)
			}
			request := &pb.UpdateSettingsRequest{Settings: &pb.Settings{LogRetentionHours: hours}, FieldMask: []string{"log_retention_hours"}, ExpectedRevision: snapshot.Settings.Revision, OperationId: "retention", RequestedAt: timestamppb.Now()}
			if _, err = c.UpdateSettings(ctx, request); err != nil {
				t.Fatal(err)
			}
			stopLogsTestController(t, c)
			c, _ = newLogsTestController(t, dir)
			snapshot, err = c.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
			if err != nil || snapshot.Settings.LogRetentionHours != hours {
				t.Fatalf("hour value lost: %v", err)
			}
			if _, err = c.GetLogDetail(ctx, &pb.LogDetailRequest{EventId: "30m0s"}); err != nil {
				t.Fatal("recent event removed", err)
			}
			if _, err = c.GetLogDetail(ctx, &pb.LogDetailRequest{EventId: (time.Duration(hours)*time.Hour + time.Minute).String()}); err == nil {
				t.Fatal("expired event retained")
			}
			request.Settings.LogRetentionHours = 168
			request.ExpectedRevision = snapshot.Settings.Revision
			request.OperationId = "invalid"
			if _, err = c.UpdateSettings(ctx, request); err == nil {
				t.Fatal("unsupported retention accepted")
			}
		})
	}
}

func TestLegacyRetentionNormalizesToSupportedHours(t *testing.T) {
	for days, hours := range map[int]int{0: 24, 1: 24, 7: 24, 30: 24} {
		meta := historyMetadata{Retention: days}
		meta.normalizeRetention()
		if meta.RetentionHours != hours {
			t.Fatalf("days=%d hours=%d", days, meta.RetentionHours)
		}
	}
}

func TestRetentionRejectsFormerLongPeriods(t *testing.T) {
	ctx := context.Background()
	c, _ := newLogsTestController(t, newLogsTestDir(t))
	snap, e := c.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
	if e != nil {
		t.Fatal(e)
	}
	for _, hours := range []uint32{0, 72, 720} {
		if _, e = c.UpdateSettings(ctx, &pb.UpdateSettingsRequest{Settings: &pb.Settings{LogRetentionHours: hours}, FieldMask: []string{"log_retention_hours"}, ExpectedRevision: snap.Settings.Revision, OperationId: fmt.Sprint(hours), RequestedAt: timestamppb.Now()}); e == nil {
			t.Fatalf("accepted invalid hours %d", hours)
		}
	}
}

func TestExistingLogsRetentionAbove24HoursIgnoresRetiredImportMetadata(t *testing.T) {
	ctx := context.Background()
	dir := newLogsTestDir(t)
	c, _ := newLogsTestController(t, dir)
	stopLogsTestController(t, c)
	editLogsDatabase(t, filepath.Join(dir, "history", "logs.db"), func(db *sql.DB) error {
		_, err := db.Exec("UPDATE meta SET value='72' WHERE key='retention_hours'")
		if err != nil {
			return err
		}
		_, err = db.Exec("INSERT OR REPLACE INTO meta(key,value) VALUES('migration_complete','1')")
		return err
	})
	c, p := newLogsTestController(t, dir)
	p.barrier()
	meta, e := c.logs.logs.SnapshotMeta(ctx)
	if e != nil || meta.RetentionHours != 24 {
		t.Fatalf("stored unsupported retention survived: %+v %v", meta, e)
	}
}
