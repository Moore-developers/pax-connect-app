package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	_ "modernc.org/sqlite"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

func TestLogUpdatesIgnoreTrafficAndCountOnlyMatchingNewEvents(t *testing.T) {
	ctx := context.Background()
	c, _ := newLogsTestController(t, newLogsTestDir(t))
	page, err := c.QueryLogs(ctx, &pb.LogQuery{})
	if err != nil {
		t.Fatal(err)
	}
	filter := &pb.LogQuery{Dimension: "domain", ObjectId: "match.test"}
	for _, name := range []string{"other.test", "match.test"} {
		if err = c.RecordEvent(ctx, config.HistoryEvent{ID: name, At: time.Now(), Title: "开始连接", Kind: "started", Level: "info", Flow: &config.FlowFact{Domain: name}}); err != nil {
			t.Fatal(err)
		}
	}
	q := &pb.LogUpdateRequest{Filter: filter, AfterSequence: page.HeadSequence, DeletionGeneration: page.History.LogDeletionGeneration, JournalGeneration: page.History.JournalGeneration, CountOnly: true}
	updates, err := c.QueryLogUpdates(ctx, q)
	if err != nil || updates.Count != 1 || !updates.Exact || len(updates.Items) != 0 {
		t.Fatalf("filtered count: %v %v", updates, err)
	}
	q.CountOnly = false
	items, err := c.QueryLogUpdates(ctx, q)
	if err != nil || len(items.Items) != 1 || items.Items[0].Domain != "match.test" {
		t.Fatal(items, err)
	}
	version := items.History.LogsRevision
	f := config.FlowFact{Key: "core/1/flow", StartedAt: time.Now().Add(-time.Minute), Direction: "direct", RouteID: "direct"}
	if err = FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "core", Collecting: true, Records: []config.Observation{{Session: "core", Sequence: 1, At: time.Now(), Kind: "sample", Flow: &f, Upload: 10}}}); err != nil {
		t.Fatal(err)
	}
	after, err := c.QueryLogUpdates(ctx, q)
	if err != nil || after.History.LogsRevision != version || after.History.TrafficRevision == 0 {
		t.Fatalf("traffic woke logs: %v %v", after, err)
	}
}

func TestLogUpdateCursorSurvivesReplayAndResetsAfterClear(t *testing.T) {
	ctx := context.Background()
	dir := newLogsTestDir(t)
	c, _ := newLogsTestController(t, dir)
	event := config.HistoryEvent{ID: "once", At: time.Now(), Title: "开始连接", Kind: "started", Level: "info"}
	if err := c.RecordEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	page, err := c.QueryLogs(ctx, &pb.LogQuery{})
	if err != nil {
		t.Fatal(err)
	}
	stopLogsTestController(t, c)
	c, _ = newLogsTestController(t, dir)
	if err = c.RecordEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	q := &pb.LogUpdateRequest{AfterSequence: page.HeadSequence, JournalGeneration: page.History.JournalGeneration, DeletionGeneration: page.History.LogDeletionGeneration, CountOnly: true}
	replay, err := c.QueryLogUpdates(ctx, q)
	if err != nil || replay.ResetRequired || replay.Count != 0 || replay.HighWater != page.HeadSequence {
		t.Fatal(replay, err)
	}
	if _, err = c.ClearLogs(ctx, &pb.ClearLogsRequest{OperationId: "clear", RequestedAt: timestamppb.Now()}); err != nil {
		t.Fatal(err)
	}
	cleared, err := c.QueryLogUpdates(ctx, q)
	if err != nil || !cleared.ResetRequired {
		t.Fatal(cleared, err)
	}
	event.ID = "after-clear"
	event.At = time.Now()
	if err = c.RecordEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	latest, err := c.QueryLogs(ctx, &pb.LogQuery{})
	if err != nil || latest.HeadSequence <= page.HeadSequence || len(latest.Items) != 2 {
		t.Fatal(latest, err)
	}
}

func TestLogJournalRebuildsEqualCountRollbackChanges(t *testing.T) {
	ctx := context.Background()
	dir := newLogsTestDir(t)
	c, _ := newLogsTestController(t, dir)
	original := config.HistoryEvent{ID: "before", At: time.Now(), Title: "旧事件", Kind: "started", Level: "info"}
	if err := c.RecordEvent(ctx, original); err != nil {
		t.Fatal(err)
	}
	before, err := c.QueryLogs(ctx, &pb.LogQuery{})
	if err != nil {
		t.Fatal(err)
	}
	stopLogsTestController(t, c)
	// Emulate an older writer that knows events but not the journal generation: the event
	// identity changes while the row count stays equal. Reopening must rebuild the
	// generation so incremental readers reset instead of trusting their cursor.
	editLogsDatabase(t, filepath.Join(dir, "history", "logs.db"), func(db *sql.DB) error {
		var body []byte
		if err := db.QueryRowContext(ctx, "SELECT body FROM events WHERE event_id='before'").Scan(&body); err != nil {
			return err
		}
		var event config.HistoryEvent
		if err := json.Unmarshal(body, &event); err != nil {
			return err
		}
		event.ID = "after-rollback"
		raw, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err = db.ExecContext(ctx, "UPDATE events SET event_id='after-rollback', body=? WHERE event_id='before'", raw); err != nil {
			return err
		}
		_, err = db.ExecContext(ctx, "UPDATE meta SET value='' WHERE key=?", historystore.MetaJournalGeneration)
		return err
	})
	c, _ = newLogsTestController(t, dir)
	rebuilt, err := c.QueryLogs(ctx, &pb.LogQuery{})
	if err != nil || rebuilt.History.JournalGeneration == before.History.JournalGeneration || len(rebuilt.Items) != 1 || rebuilt.Items[0].EventId != "after-rollback" {
		t.Fatal(rebuilt, err)
	}
	stopLogsTestController(t, c)
	c, _ = newLogsTestController(t, dir)
	stable, err := c.QueryLogs(ctx, &pb.LogQuery{})
	if err != nil || stable.HeadSequence != rebuilt.HeadSequence || stable.History.JournalGeneration != rebuilt.History.JournalGeneration {
		t.Fatal(stable, err)
	}
}

// stopLogsTestController closes the log writer and the legacy owner before a reopen.
func stopLogsTestController(t *testing.T, c *Controller) {
	_ = c.CloseHistoryTraffic(context.Background())
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.CloseHistoryLogs(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.CloseHistory(ctx); err != nil {
		t.Fatal(err)
	}
}

// editLogsDatabase mutates logs.db between writer sessions, standing in for an older
// writer that left the file in a shape this version must recover from.
func editLogsDatabase(t *testing.T, path string, edit func(*sql.DB) error) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err = edit(db); err != nil {
		t.Fatal(err)
	}
}
