package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

func TestLogsDiagnosticsKeepNewestEight(t *testing.T) {
	h := newLogsHarness(t)
	f := testLogFlow("s/1/a")
	var rows []config.Observation
	for i := 1; i <= 10; i++ {
		rows = append(rows, logObservation(uint64(i), "diagnostic", cloneLogFlow(f), 0, 0, "connection reset by peer", "error"))
	}
	h.appendRows(rows)
	h.settle()
	var count, first, last int64
	if err := h.logs.Reader().QueryRow("SELECT count(*),min(seq),max(seq) FROM diagnostics WHERE flow_key=?", f.Key).Scan(&count, &first, &last); err != nil {
		t.Fatal(err)
	}
	if count != 8 || first != 3 || last != 10 {
		t.Fatalf("diagnostics kept %d rows %d..%d, want 8 rows 3..10", count, first, last)
	}
}

func TestLogsPaxInboxOverflowIsRecordedAndEventsCarryPaxSource(t *testing.T) {
	ctx := context.Background()
	h := newLogsHarness(t)
	for i := 0; i < logsInboxCapacity+1; i++ {
		h.l.enqueue(config.HistoryEvent{ID: fmt.Sprintf("pax:%d", i), At: logsEpoch, Kind: "operation",
			Source: "Pax", Level: "info", Title: "操作完成"})
	}
	if d := h.l.step(ctx); d != logsPollInterval {
		t.Fatalf("step after overflow: %v", d)
	}
	var count, wrongSource int
	if err := h.logs.Reader().QueryRow("SELECT count(*),count(*) FILTER (WHERE source<>'Pax') FROM events").Scan(&count, &wrongSource); err != nil {
		t.Fatal(err)
	}
	if count != logsInboxCapacity || wrongSource != 0 {
		t.Fatalf("events %d, non-Pax %d", count, wrongSource)
	}
	rows, err := h.logs.Reader().Query("SELECT seq,body FROM events ORDER BY seq")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var seq int64
		var body []byte
		if err = rows.Scan(&seq, &body); err != nil {
			t.Fatal(err)
		}
		var e config.HistoryEvent
		if err = json.Unmarshal(body, &e); err != nil {
			t.Fatal(err)
		}
		if e.Sequence != uint64(seq) || e.Source != "Pax" {
			t.Fatalf("event body sequence/source: %d %q", e.Sequence, e.Source)
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := h.logs.Meta(ctx, historystore.MetaPartial); err != nil || !ok || v != "1" {
		t.Fatalf("partial: %q %v %v", v, ok, err)
	}
	if v, ok, err := h.logs.Meta(ctx, historystore.MetaErrorCode); err != nil || !ok || v != logsErrorIngressFull {
		t.Fatalf("error code: %q %v %v", v, ok, err)
	}
}

func TestLogsRetentionAndCapacityMaintenance(t *testing.T) {
	ctx := context.Background()
	h := newLogsHarness(t)
	if err := h.logs.Update(ctx, func(tx *historystore.LogsTx) error {
		return tx.SetMeta(ctx, historystore.MetaRetentionHours, "1")
	}); err != nil {
		t.Fatal(err)
	}
	old := logObservation(1, "started", testLogFlow("s/1/old"), 0, 0, "", "")
	old.At = logsEpoch.Add(-2 * time.Hour)
	h.appendRows([]config.Observation{old})
	h.settle()
	for _, table := range []string{"connections", "events", "diagnostics"} {
		var n int
		if err := h.logs.Reader().QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%s kept %d expired rows", table, n)
		}
	}
	// One recent row is enough to cross a one-byte cap; eviction is reported, not refused.
	h.l.capBytes = 1
	h.appendRows([]config.Observation{logObservation(2, "started", testLogFlow("s/1/new"), 0, 0, "", "")})
	h.settle()
	var connections int
	if err := h.logs.Reader().QueryRow("SELECT count(*) FROM connections").Scan(&connections); err != nil {
		t.Fatal(err)
	}
	if connections != 0 {
		t.Fatalf("capacity eviction left %d connections", connections)
	}
	if v, ok, err := h.logs.Meta(ctx, historystore.MetaCapacityEvicted); err != nil || !ok || v != "1" {
		t.Fatalf("capacity_evicted: %q %v %v", v, ok, err)
	}
}

type flakyLogs struct {
	*historystore.Logs
	fail int
}

func (f *flakyLogs) Update(ctx context.Context, fn func(*historystore.LogsTx) error) error {
	if f.fail > 0 {
		f.fail--
		return errors.New("disk full")
	}
	return f.Logs.Update(ctx, fn)
}

func TestLogsWriteFailureBacksOffWithoutAdvancingCursor(t *testing.T) {
	ctx := context.Background()
	h := newLogsHarness(t)
	first := []config.Observation{
		logObservation(1, "started", testLogFlow("s/1/a"), 0, 0, "", ""),
		logObservation(2, "sample", testLogFlow("s/1/a"), 3, 30, "", ""),
		logObservation(3, "closed", testLogFlow("s/1/a"), 0, 0, "", ""),
	}
	h.appendRows(first)
	h.settle()
	more := []config.Observation{
		logObservation(4, "started", testLogFlow("s/1/b"), 0, 0, "", ""),
		logObservation(5, "sample", testLogFlow("s/1/b"), 4, 40, "", ""),
	}
	h.appendRows(more)
	h.l.logs = &flakyLogs{Logs: h.logs, fail: 1}
	if d := h.l.step(ctx); d != time.Second {
		t.Fatalf("first failure delay %v, want 1s", d)
	}
	if h.l.state.cursor != (historystore.Cursor{Session: "s", Seq: 3}) {
		t.Fatalf("cursor advanced on a failed batch: %+v", h.l.state.cursor)
	}
	if n := countRows(t, h.logs, "events"); n != 2 {
		t.Fatalf("events after failed batch: %d", n)
	}
	if v, ok, err := h.logs.Meta(ctx, historystore.MetaPartial); err != nil || !ok || v != "1" {
		t.Fatalf("partial after failure: %q %v %v", v, ok, err)
	}
	if v, ok, err := h.logs.Meta(ctx, historystore.MetaErrorCode); err != nil || !ok || v != logsErrorStore {
		t.Fatalf("error code after failure: %q %v %v", v, ok, err)
	}
	if d := h.l.step(ctx); d != logsPollInterval {
		t.Fatalf("recovery delay %v, want poll interval", d)
	}
	if h.l.state.cursor != (historystore.Cursor{Session: "s", Seq: 5}) {
		t.Fatalf("cursor after recovery: %+v", h.l.state.cursor)
	}
	if n := countRows(t, h.logs, "events"); n != 3 {
		t.Fatalf("events after recovery: %d", n)
	}
	if v, ok, err := h.logs.Meta(ctx, historystore.MetaErrorCode); err != nil || !ok || v != "" {
		t.Fatalf("error code not cleared: %q %v %v", v, ok, err)
	}
}

func TestLogsWriteFailureBackoffDoublesToSixtySeconds(t *testing.T) {
	ctx := context.Background()
	h := newLogsHarness(t)
	h.appendRows([]config.Observation{logObservation(1, "started", testLogFlow("s/1/a"), 0, 0, "", "")})
	h.l.logs = &flakyLogs{Logs: h.logs, fail: 100}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, 60 * time.Second, 60 * time.Second}
	for i, expected := range want {
		if d := h.l.step(ctx); d != expected {
			t.Fatalf("backoff %d = %v, want %v", i, d, expected)
		}
	}
}

func TestLogsRestartResumesFromCommittedCursor(t *testing.T) {
	ctx := context.Background()
	h := newLogsHarness(t)
	first := []config.Observation{
		logObservation(1, "started", testLogFlow("s/1/a"), 0, 0, "", ""),
		logObservation(2, "ready", testLogFlow("s/1/a"), 0, 0, "", ""),
		logObservation(3, "closed", testLogFlow("s/1/a"), 0, 0, "", ""),
	}
	h.appendRows(first)
	h.settle()
	if err := h.l.stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.journal.Close(); err != nil {
		t.Fatal(err)
	}
	uid := uint32(os.Geteuid())
	j, err := historystore.OpenJournal(h.dir, uid)
	if err != nil {
		t.Fatal(err)
	}
	logs, _, err := historystore.OpenLogs(ctx, h.dir, uid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logs.Close(); _ = j.Close() })
	consumers := newJournalConsumers()
	if err = consumers.Register(HistoryConsumerLogs, historystore.Cursor{}); err != nil {
		t.Fatal(err)
	}
	l, err := newHistoryLogs(ctx, logs, j, consumers)
	if err != nil {
		t.Fatal(err)
	}
	l.now = func() time.Time { return h.clock }
	h.journal, h.logs, h.l = j, logs, l
	more := []config.Observation{
		logObservation(4, "started", testLogFlow("s/1/b"), 0, 0, "", ""),
		logObservation(5, "ready", testLogFlow("s/1/b"), 0, 0, "", ""),
	}
	h.appendRows(more)
	h.settle()
	if l.state.cursor != (historystore.Cursor{Session: "s", Seq: 5}) {
		t.Fatalf("restored cursor %+v", l.state.cursor)
	}
	var total, distinct int
	if err = logs.Reader().QueryRow("SELECT count(*),count(DISTINCT event_id) FROM events").Scan(&total, &distinct); err != nil {
		t.Fatal(err)
	}
	if total != 5 || distinct != 5 {
		t.Fatalf("events %d, distinct %d", total, distinct)
	}
}

func TestIngestCommitCarriesPostGapBatchBound(t *testing.T) {
	h := newIngestHarness(t)
	h.in.pending = &journalGap{Reason: gapLost, Session: "S1", FromSeq: 4, ToSeq: 5}
	record := config.Observation{Session: "S1", Sequence: 6, At: testEpoch, Kind: "started", Flow: flow("n")}
	plan := ingestPlan{rows: []historystore.Observation{journalRecord(record)}, next: historystore.Cursor{Session: "S1", Seq: 6}}
	if err := h.in.commit(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	gaps := h.gaps()
	if len(gaps) != 1 || gaps[0].Reason != gapLost || gaps[0].ThroughSeq != 6 {
		t.Fatalf("gap batch bound: %+v", gaps)
	}
}

func TestEnqueueHistoryEventReachesLogJobWithSystemEvidence(t *testing.T) {
	ctx := context.Background()
	h := newLogsHarness(t)
	c := New("test", "test")
	c.logs = h.l
	event := config.HistoryEvent{ID: "pax:explicit", At: logsEpoch, Kind: "operation", Source: "Pax", Level: "info",
		Title: "更新信任策略", System: &config.SystemEvent{Type: "trust.policy_changed", Name: "更新信任策略",
			Category: config.EventCategory{Key: "trust", Name: "证书与信任", Symbol: "checkmark.shield"},
			Result:   "completed", Sections: []config.EventSection{{Kind: "facts", Title: "事实", Fields: []config.EventField{{Label: "来源", Value: "系统"}}}}}}
	c.enqueueHistoryEvent(event)
	if d := h.l.step(ctx); d != logsPollInterval {
		t.Fatalf("step after enqueue: %v", d)
	}
	var body []byte
	if err := h.logs.Reader().QueryRow("SELECT body FROM events WHERE event_id=?", event.ID).Scan(&body); err != nil {
		t.Fatal(err)
	}
	var stored config.HistoryEvent
	if err := json.Unmarshal(body, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Source != "Pax" || stored.System == nil || stored.System.Category.Key != "trust" || stored.System.Category.Name != "证书与信任" {
		t.Fatalf("Pax event evidence lost: %+v", stored)
	}
}

func TestConfigureHistoryLogsOpensAndStops(t *testing.T) {
	ctx := context.Background()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(base, 0700); err != nil {
		t.Fatal(err)
	}
	c := New("test", "test")
	if err = c.ConfigureHistoryConsumers(HistoryConsumerLogs); err != nil {
		t.Fatal(err)
	}
	if err = c.ConfigureHistoryLogs(ctx, base, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	closeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err = c.CloseHistoryLogs(closeCtx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(base, "history", "logs.db")); err != nil {
		t.Fatal(err)
	}
	if c.logs != nil {
		t.Fatal("log job not cleared")
	}
}

func countRows(t *testing.T, logs *historystore.Logs, table string) int {
	t.Helper()
	var n int
	if err := logs.Reader().QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestLogsDrainFailsWhileBackoffIsAtItsCap(t *testing.T) {
	ctx := context.Background()
	h := newLogsHarness(t)
	h.appendRows([]config.Observation{logObservation(1, "started", testLogFlow("s/1/a"), 0, 0, "", "")})
	h.l.logs = &flakyLogs{Logs: h.logs, fail: 1000}
	for range 8 {
		h.l.step(ctx)
	}
	if err := h.l.drain(ctx); err == nil {
		t.Fatal("drain reported success while the journal could not be committed")
	}
	if h.l.state.cursor != (historystore.Cursor{}) {
		t.Fatalf("cursor advanced by a failed drain: %+v", h.l.state.cursor)
	}
}

func TestLogsDrainFailsWhileDegradedMetadataCannotBeCleared(t *testing.T) {
	ctx := context.Background()
	h := newLogsHarness(t)
	if err := h.logs.Update(ctx, func(tx *historystore.LogsTx) error {
		return tx.SetMeta(ctx, historystore.MetaErrorCode, logsErrorStore)
	}); err != nil {
		t.Fatal(err)
	}
	h.l.down = true // the journal is empty: only the stale error marker is left to clear
	h.l.logs = &flakyLogs{Logs: h.logs, fail: 1000}
	if err := h.l.drain(ctx); err == nil {
		t.Fatal("drain reported success while the degraded marker could not be cleared")
	}
	h.l.logs = h.logs
	if err := h.l.drain(ctx); err != nil {
		t.Fatalf("drain after the metadata write recovered: %v", err)
	}
	if v, ok, err := h.logs.Meta(ctx, historystore.MetaErrorCode); err != nil || !ok || v != "" || h.l.down {
		t.Fatalf("error marker not cleared: %q %v %v", v, ok, err)
	}
}
