package controller

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// A writer published through logJob must already be fully started: submit reads its
// channels from another goroutine, and -race fails if start() still writes them afterwards.
func TestConfigureHistoryLogsPublishesStartedWriter(t *testing.T) {
	ctx := context.Background()
	base := newLogsTestDir(t)
	c := New("test", "test")
	if err := c.ConfigureHistoryConsumers(HistoryConsumerLogs); err != nil {
		t.Fatal(err)
	}
	flushed := make(chan error, 1)
	go func() {
		for {
			job, err := c.logJob()
			if err != nil {
				time.Sleep(50 * time.Microsecond)
				continue
			}
			flushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			flushed <- job.Flush(flushCtx)
			return
		}
	}()
	if err := c.ConfigureHistoryLogs(ctx, base, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopLogsTestController(t, c) })
	if err := <-flushed; err != nil {
		t.Fatalf("flush through the published writer: %v", err)
	}
}

// stopWriterGoroutine ends the writer goroutine but keeps logs.db open, so a test can drive
// the writer's methods itself without any concurrent access.
func stopWriterGoroutine(t *testing.T, c *Controller) *historyLogs {
	t.Helper()
	l, err := c.logJob()
	if err != nil {
		t.Fatal(err)
	}
	l.cancel()
	<-l.done
	return l
}

// A deletion committed by maintenance must reach the read cache at the commit, not at the
// next publishState: a fresh query in between must succeed and an older token must expire.
func TestLogsDeletionCommitReachesReadCacheBeforePublish(t *testing.T) {
	ctx := context.Background()
	c, p := newLogsTestController(t, newLogsTestDir(t))
	p.emit(config.HistoryEvent{ID: "keep", At: time.Now(), Kind: "operation", Title: "保留", Level: "info", Source: "Pax"})
	first, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity"})
	if err != nil || first.ReadToken == "" {
		t.Fatalf("first page: %v", err)
	}
	l := stopWriterGoroutine(t, c)
	if err = l.bumpDeletion(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = c.QueryLogs(ctx, &pb.LogQuery{View: "activity", ReadToken: first.ReadToken}); err == nil {
		t.Fatal("token from before the deletion survived")
	}
	page, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity"})
	if err != nil || page.Total != 1 {
		t.Fatalf("query right after the deletion commit: total=%d err=%v", page.GetTotal(), err)
	}
}

// stop() drains the inbox: an event queued without waking the writer is still committed.
func TestLogsStopFlushesInbox(t *testing.T) {
	ctx := context.Background()
	dir := newLogsTestDir(t)
	c, p := newLogsTestController(t, dir)
	p.barrier()
	l, err := c.logJob()
	if err != nil {
		t.Fatal(err)
	}
	l.inbox <- config.HistoryEvent{ID: "queued-at-stop", At: time.Now(), Kind: "operation", Title: "关闭前入队", Level: "info", Source: "Pax"}
	stopLogsTestController(t, c)
	logs, _, err := historystore.OpenLogs(ctx, dir+"/history", uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logs.Close() }()
	if n := countRows(t, logs, "events"); n != 1 {
		t.Fatalf("events after stop = %d, want 1", n)
	}
}

// Once the writer has stopped, submit answers history_unavailable at once instead of
// waiting for the caller's context.
func TestLogsSubmitAfterStopFailsFast(t *testing.T) {
	c, _ := newLogsTestController(t, newLogsTestDir(t))
	l := stopWriterGoroutine(t, c)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	err := l.Flush(ctx)
	if err == nil || err.Error() != "history_unavailable" {
		t.Fatalf("flush after stop: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("flush after stop waited %v", elapsed)
	}
}

// pruneFailing makes the first prunes fail, standing in for a store error mid-command.
type pruneFailing struct {
	logsStore
	fail bool
}

func (p *pruneFailing) Prune(ctx context.Context, before time.Time, limits historystore.Limits) (historystore.Pruned, error) {
	if p.fail {
		return historystore.Pruned{}, errors.New("disk failure")
	}
	return p.logsStore.Prune(ctx, before, limits)
}

// The idempotency receipt is written only once pruning succeeded: the same operation id
// retried after a failed prune must prune again instead of being answered from a receipt.
func TestLogsSettingsRetryAfterFailedPruneRunsAgain(t *testing.T) {
	ctx := context.Background()
	h := newLogsHarness(t)
	h.l.enqueue(config.HistoryEvent{ID: "old", At: h.clock.Add(-5 * time.Hour), Kind: "operation", Title: "旧", Level: "info", Source: "Pax"})
	h.l.step(ctx)
	if n := countRows(t, h.logs, "events"); n != 1 {
		t.Fatalf("seeded events = %d", n)
	}
	flaky := &pruneFailing{logsStore: h.l.logs, fail: true}
	h.l.logs = flaky
	cmd := logsCommand{kind: logsCommandSettings, id: "retention-1", signature: "retention-1h", at: h.clock, hours: 1}
	if result := h.l.executeSettings(ctx, cmd); result.err == nil || result.err.Error() != logsErrorStore {
		t.Fatalf("failed prune result: %v", result.err)
	}
	if n := countRows(t, h.logs, "commands"); n != 0 {
		t.Fatalf("receipt recorded for unfinished work: %d", n)
	}
	if meta, err := h.logs.SnapshotMeta(ctx); err != nil || meta.RetentionHours != 24 || meta.SettingsRevision != 0 {
		t.Fatalf("settings changed by a failed command: %+v %v", meta, err)
	}
	flaky.fail = false
	for attempt := 1; attempt <= 2; attempt++ {
		if result := h.l.executeSettings(ctx, cmd); result.err != nil {
			t.Fatalf("retry %d: %v", attempt, result.err)
		}
		meta, err := h.logs.SnapshotMeta(ctx)
		if err != nil || meta.RetentionHours != 1 || meta.SettingsRevision != 1 || countRows(t, h.logs, "events") != 0 || countRows(t, h.logs, "commands") != 1 {
			t.Fatalf("after retry %d: meta=%+v events=%d commands=%d err=%v", attempt, meta, countRows(t, h.logs, "events"), countRows(t, h.logs, "commands"), err)
		}
	}
}

// Watch overlays quality fields from logs.db alone; the legacy state no longer ORs in.
func TestMergeLogHistoryTakesQualityFromLogsOnly(t *testing.T) {
	base := &pb.HistoryState{Partial: true, CapacityEvicted: true, ErrorCode: "legacy_error", LogsRevision: 9}
	got := mergeLogHistory(base, historystore.LogsMeta{LogsRevision: 3})
	if got.Partial || got.CapacityEvicted || got.ErrorCode != "" || got.LogsRevision != 3 {
		t.Fatalf("legacy quality leaked into the log overlay: %+v", got)
	}
	got = mergeLogHistory(&pb.HistoryState{}, historystore.LogsMeta{Partial: true, CapacityEvicted: true, ErrorCode: logsErrorStore})
	if !got.Partial || !got.CapacityEvicted || got.ErrorCode != logsErrorStore {
		t.Fatalf("logs.db quality lost: %+v", got)
	}
}

// Raw lines live in their own column: the body list scans read carries none, and event
// detail gets them back unchanged.
func TestLogsRawLinesStayOutOfListBodies(t *testing.T) {
	ctx := context.Background()
	c, p := newLogsTestController(t, newLogsTestDir(t))
	lines := []string{"connection reset by peer", "dial tcp: i/o timeout"}
	p.emit(config.HistoryEvent{ID: "with-raw", At: time.Now(), Kind: "diagnostic", Title: "诊断", Level: "error", Source: "fixture", Raw: lines})
	logs := c.logs.logs.(*historystore.Logs)
	var body, raw []byte
	if err := logs.Reader().QueryRow("SELECT body,raw FROM events WHERE event_id='with-raw'").Scan(&body, &raw); err != nil {
		t.Fatal(err)
	}
	var stored config.HistoryEvent
	if err := json.Unmarshal(body, &stored); err != nil || len(stored.Raw) != 0 {
		t.Fatalf("list body carries raw lines: %v %v", stored.Raw, err)
	}
	var column []string
	if err := json.Unmarshal(raw, &column); err != nil || !reflect.DeepEqual(column, lines) {
		t.Fatalf("raw column = %q (%v), want %q", column, err, lines)
	}
	detail, err := c.GetLogDetail(ctx, &pb.LogDetailRequest{EventId: "with-raw"})
	if err != nil || !reflect.DeepEqual(detail.Raw, lines) {
		t.Fatalf("detail raw = %q (%v), want %q", detail.GetRaw(), err, lines)
	}
}
