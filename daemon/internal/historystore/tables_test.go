package historystore

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
)

func TestLogsUpdateCommitsRowsAndCursorAtomically(t *testing.T) {
	l := mustLogs(t, historyDir(t))
	ctx := context.Background()
	fail := errors.New("boom")
	err := l.Update(ctx, func(tx *LogsTx) error {
		if err := tx.PutSummary(ctx, config.LogConnectionSummary{Fact: config.FlowFact{Key: "k"}, LastLogAt: time.UnixMilli(1)}); err != nil {
			return err
		}
		if err := tx.SetCursor(ctx, "logs", Cursor{"s", 7}); err != nil {
			return err
		}
		return fail
	})
	if !errors.Is(err, fail) {
		t.Fatal(err)
	}
	if _, ok, _ := l.Cursor(ctx, "logs"); ok || count(t, l.Reader(), "SELECT count(*) FROM col_connections") != 0 {
		t.Fatal("failed update left rows or cursor behind")
	}
	summary := config.LogConnectionSummary{Fact: config.FlowFact{Key: "k", StartedAt: time.UnixMilli(1), ApplicationID: "a", Domain: "d", IP: "1.1.1.1", Failure: "x"}, LastLogAt: time.UnixMilli(3)}
	want := summary
	value := config.HistoryEvent{ID: "s:1", At: time.UnixMilli(1), Kind: "started", Level: "info", Source: "sing-box", Flow: &summary.Fact}
	event := value
	err = l.Update(ctx, func(tx *LogsTx) error {
		for _, step := range []error{
			tx.PutSummary(ctx, want), tx.PutSummary(ctx, want),
			tx.PutDiagnostic(ctx, LogDiagnostic{FlowKey: "k", Seq: 1, AtMs: 1, Text: "t"}),
			tx.SetCursor(ctx, "logs", Cursor{"s", 7}), tx.SetMeta(ctx, "m", "v"),
		} {
			if step != nil {
				return step
			}
		}
		inserted, seq, err := tx.AppendEvent(ctx, event)
		if err != nil || !inserted || seq != 1 {
			return errors.New("first event not inserted as sequence 1")
		}
		if inserted, seq, err = tx.AppendEvent(ctx, event); err != nil || inserted || seq != 0 {
			return errors.New("duplicate event was inserted or numbered")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := l.Columns().Connection(ctx, "k")
	if err != nil || !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("connection roundtrip: %+v %v", got, err)
	}
	for table, n := range map[string]int{"col_connections": 1, "col_events": 1, "diagnostics": 1, "commands": 0} {
		if c := count(t, l.Reader(), "SELECT count(*) FROM "+table); c != n {
			t.Fatalf("%s: %d rows", table, c)
		}
	}
	stored, ok, err := l.Columns().Event(ctx, event.ID)
	if err != nil || !ok {
		t.Fatal(stored, ok, err)
	}
	gotEvent := stored
	value.Sequence = 1
	if !reflect.DeepEqual(gotEvent, value) {
		t.Fatalf("event roundtrip: %+v want %+v", gotEvent, value)
	}
	if c, ok, err := l.Cursor(ctx, "logs"); err != nil || !ok || c != (Cursor{"s", 7}) {
		t.Fatalf("cursor: %+v %v %v", c, ok, err)
	}
	if v, ok, err := l.Meta(ctx, "m"); err != nil || !ok || v != "v" {
		t.Fatalf("meta: %q %v %v", v, ok, err)
	}
	if err = l.Update(ctx, func(tx *LogsTx) error { return tx.SetCursor(ctx, "logs", Cursor{"s", 1 << 63}) }); !errors.Is(err, ErrCursorRange) {
		t.Fatalf("oversized cursor: %v", err)
	}
}

func TestLogsDefaultsEventsAndDiagnosticsBounds(t *testing.T) {
	l := mustLogs(t, historyDir(t))
	ctx := context.Background()
	for key, want := range map[string]string{MetaRetentionHours: "24", MetaPartial: "0", MetaErrorCode: "", MetaCapacityEvicted: "0"} {
		if got, ok, err := l.Meta(ctx, key); err != nil || !ok || got != want {
			t.Fatalf("default %s: %q %v %v", key, got, ok, err)
		}
	}
	err := l.Update(ctx, func(tx *LogsTx) error {
		for i := range 10 {
			if err := tx.PutDiagnostic(ctx, LogDiagnostic{FlowKey: "k", Seq: int64(i), AtMs: int64(i), Text: "t"}); err != nil {
				return err
			}
		}
		if err := tx.TrimDiagnostics(ctx, "k", 8); err != nil {
			return err
		}
		for i := range 3 {
			inserted, seq, err := tx.AppendEvent(ctx, config.HistoryEvent{ID: "e" + string(rune('a'+i)), At: time.UnixMilli(int64(i))})
			if err != nil || !inserted || seq != int64(i+1) {
				return errors.New("event sequence is not dense from 1")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var first, last int64
	if err = l.Reader().QueryRow("SELECT min(seq),max(seq) FROM diagnostics WHERE flow_key='k'").Scan(&first, &last); err != nil || first != 2 || last != 9 {
		t.Fatalf("diagnostics trimmed to %d..%d: %v", first, last, err)
	}
	if n := count(t, l.Reader(), "SELECT count(*) FROM diagnostics WHERE flow_key='k'"); n != 8 {
		t.Fatalf("diagnostics: %d", n)
	}
	// A replay after reopen is still a no-op and does not consume a sequence.
	if err = l.Update(ctx, func(tx *LogsTx) error {
		if inserted, _, err := tx.AppendEvent(ctx, config.HistoryEvent{ID: "ea", At: time.UnixMilli(0)}); err != nil || inserted {
			return errors.New("replayed event inserted")
		}
		exists, err := tx.EventExists(ctx, "ea")
		if err != nil || !exists {
			return errors.New("event lookup failed")
		}
		inserted, seq, err := tx.AppendEvent(ctx, config.HistoryEvent{ID: "ed", At: time.UnixMilli(9)})
		if err != nil || !inserted || seq != 4 {
			return errors.New("event sequence advanced by a replay")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTrafficRowsCoverageAndMarksRoundTrip(t *testing.T) {
	tr := mustTraffic(t, historyDir(t))
	ctx := context.Background()
	key := TrafficKey{BucketMs: 300000, Dimension: "app", Object: "o", Route: "r", Direction: "out"}
	want := TrafficRow{TrafficKey: key, Name: "n", SourceKind: "app", RouteName: "R", UpBytes: 1, DownBytes: 2, NewConns: 3, Failures: 4,
		LatencyMs: 5, LatencyAtMs: 6, MaxLatencyMs: 7, LatencyHist: []byte{1, 2}, RuleBasis: []byte{3}, Search: "s"}
	mark := FlowMark{FlowKey: "c", Started: true, Responded: true, LastAtMs: 9}
	err := tr.Update(ctx, func(tx *TrafficTx) error {
		if _, ok, err := tx.Traffic5m(ctx, key); ok || err != nil {
			return errors.New("unexpected row")
		}
		for _, step := range []error{tx.PutTraffic5m(ctx, want), tx.PutRecent5s(ctx, want), tx.PutCoverage(ctx, Coverage{BucketMs: 300000, Bits: []byte{0xff}}),
			tx.PutFlowMark(ctx, mark), tx.SetCursor(ctx, "traffic", Cursor{"s", 3})} {
			if step != nil {
				return step
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = tr.Update(ctx, func(tx *TrafficTx) error {
		for name, load := range map[string]func() (TrafficRow, bool, error){
			"5m": func() (TrafficRow, bool, error) { return tx.Traffic5m(ctx, key) },
			"5s": func() (TrafficRow, bool, error) { return tx.Recent5s(ctx, key) },
		} {
			if got, ok, err := load(); err != nil || !ok || !reflect.DeepEqual(got, want) {
				return errors.New(name + " round trip mismatch")
			}
		}
		if c, ok, err := tx.Coverage(ctx, 300000); err != nil || !ok || !reflect.DeepEqual(c.Bits, []byte{0xff}) {
			return errors.New("coverage mismatch")
		}
		if m, ok, err := tx.FlowMark(ctx, "c"); err != nil || !ok || m != mark {
			return errors.New("flow mark mismatch")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if c, ok, _ := tr.Cursor(ctx, "traffic"); !ok || c != (Cursor{"s", 3}) {
		t.Fatalf("cursor %+v", c)
	}
}
