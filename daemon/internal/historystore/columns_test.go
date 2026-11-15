package historystore

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
)

// Acceptance: every stored field, nullable pointer, zero time and ordered list
// survives the fact mapping; updating a summary cannot mutate an earlier event.
func TestColumnsFactsRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := historyDir(t)
	c, err := OpenColumns(ctx, dir, uid())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	at := time.Date(2026, 11, 14, 3, 12, 4, 123456789, time.FixedZone("", -8*3600))
	closed := at.Add(time.Second)
	f := config.FlowFact{RoutingMatchedAt: at, Phase: "closed", Purpose: "dns", IPSource: "fakeip", ProcessStatus: "known", DNS: &config.DNSFact{QueryType: "A", Result: "success", Answers: []string{"2.2.2.2", "1.1.1.1", "2.2.2.2"}}, ResponseKnown: true, ResponseMS: 0, ObservedUntil: &at, StateUnknown: true, Key: "f", ApplicationID: "app", Application: "应用", Process: "/Applications/App", Domain: "example.test", IP: "1.1.1.1", Port: 65535, Transport: "udp", RouteID: "r", RouteName: "route", Direction: "proxy", Rule: "rule", RuleSource: "rule-source", StartedAt: at, ClosedAt: &closed, EstablishmentState: "failed", EstablishmentMS: math.MaxUint64, Failure: "failure", Evidence: "evidence"}
	m := config.ConnectionMetrics{DurationMS: math.MaxUint64, DurationState: "partial", Upload: math.MaxUint64, Download: 1234, BytesState: "known", BytesFrom: at, AsOf: closed, Reason: "gap"}
	original := config.HistoryEvent{Sequence: 1, ID: "event", At: at, Kind: "operation", Title: "标题", Level: "warning", Source: "Pax", Result: "failed", OperationID: "op", OperationFrom: "old", OperationTo: "new", Metrics: &m, Flow: &f, Raw: []string{"second", "first", ""}, Truncated: true, RawOmitted: "reason", System: &config.SystemEvent{Type: "type", Name: "name", Result: "failed", ErrorCode: "error", Category: config.EventCategory{Key: "category", Name: "分类", Symbol: "gear"}, StartedAt: &at, Sections: []config.EventSection{{Kind: "changes", Title: "section", Fields: []config.EventField{{Label: "label", Value: "value", Before: "before", After: "after"}, {Label: "duplicate", Value: "value"}}}, {Kind: "facts", Fields: []config.EventField{}}, {Kind: "note"}}}}
	// Legacy input is JSON, so normalize time.Location exactly as that boundary does.
	b, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var e config.HistoryEvent
	if err = json.Unmarshal(b, &e); err != nil {
		t.Fatal(err)
	}
	summary := config.LogConnectionSummary{Fact: *e.Flow, Metrics: *e.Metrics, LastEventID: e.ID, LastLogAt: e.At, LastSequence: e.Sequence}
	if err = c.Update(ctx, func(tx *ColumnsTx) error {
		if _, e2 := tx.PutEvent(ctx, e); e2 != nil {
			return e2
		}
		return tx.PutConnection(ctx, summary)
	}); err != nil {
		t.Fatal(err)
	}
	got, found, err := c.Event(ctx, e.ID)
	if err != nil || !found {
		t.Fatalf("event found=%v err=%v", found, err)
	}
	if !reflect.DeepEqual(got, e) {
		t.Fatalf("event differs\ngot %#v\nwant %#v", got, e)
	}
	gs, found, err := c.Connection(ctx, f.Key)
	if err != nil || !found {
		t.Fatalf("summary found=%v err=%v", found, err)
	}
	if !reflect.DeepEqual(gs, summary) {
		t.Fatalf("summary differs: %#v %#v", gs, summary)
	}
	summary.Fact.Application = "changed"
	summary.Fact.DNS = &config.DNSFact{Answers: []string{}}
	if err = c.Update(ctx, func(tx *ColumnsTx) error { return tx.PutConnection(ctx, summary) }); err != nil {
		t.Fatal(err)
	}
	got, _, err = c.Event(ctx, e.ID)
	if err != nil || !reflect.DeepEqual(got, e) {
		t.Fatalf("summary mutated earlier event: %v", err)
	}
	gs, _, err = c.Connection(ctx, f.Key)
	if err != nil || !reflect.DeepEqual(gs, summary) {
		t.Fatalf("updated summary differs: %v", err)
	}
	if n := count(t, c.r, "SELECT count(*) FROM col_identities"); n != 2 {
		t.Fatalf("identity snapshots=%d, want 2", n)
	}
	for i, empty := range []config.HistoryEvent{{Sequence: 2, ID: "nil"}, {Sequence: 3, ID: "empty", Flow: &config.FlowFact{DNS: &config.DNSFact{Answers: []string{}}}, Metrics: &config.ConnectionMetrics{}, Raw: []string{}, System: &config.SystemEvent{Sections: []config.EventSection{}}}, {Sequence: 4, ID: "nil-answers", Flow: &config.FlowFact{DNS: &config.DNSFact{}}}} {
		if err = c.Update(ctx, func(tx *ColumnsTx) error { _, er := tx.PutEvent(ctx, empty); return er }); err != nil {
			t.Fatal(err)
		}
		got, _, err = c.Event(ctx, empty.ID)
		if err != nil || !reflect.DeepEqual(got, empty) {
			t.Fatalf("empty case %d differs: %v\ngot %#v\nwant %#v", i, err, got, empty)
		}
	}
}

// Acceptance: replay and failed multi-table commits cannot leave child evidence
// or a partially updated identity behind; opening columns leaves v3 data alone.
func TestColumnsAtomicReplayAndLegacyIsolation(t *testing.T) {
	ctx := context.Background()
	dir := historyDir(t)
	if err := prepareDirectory(dir, uid()); err != nil {
		t.Fatal(err)
	}
	legacy, _, err := openStore(ctx, filepath.Join(dir, logsFile), uid(), storeSpec{ddl: logsDDL, version: 3, readers: readerConns})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = legacy.Close() }()
	if _, err := legacy.w.ExecContext(ctx, "INSERT INTO events(event_id,at_ms,body) VALUES('legacy',1,'legacy-body')"); err != nil {
		t.Fatal(err)
	}
	c, err := OpenColumns(ctx, dir, uid())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	e := config.HistoryEvent{Sequence: 1, ID: "new", Title: "original", Flow: &config.FlowFact{Key: "f", Application: "original"}, Raw: []string{"one"}}
	if err = c.Update(ctx, func(tx *ColumnsTx) error { _, er := tx.PutEvent(ctx, e); return er }); err != nil {
		t.Fatal(err)
	}
	changed := e
	changed.Title = "replay change"
	changed.Raw = []string{"changed"}
	changed.Flow = &config.FlowFact{Key: "f", Application: "changed"}
	if err = c.Update(ctx, func(tx *ColumnsTx) error {
		inserted, er := tx.PutEvent(ctx, changed)
		if inserted {
			t.Fatal("replay inserted")
		}
		return er
	}); err != nil {
		t.Fatal(err)
	}
	abort := errors.New("abort")
	if err = c.Update(ctx, func(tx *ColumnsTx) error {
		changed.ID = "rollback"
		changed.Sequence = 2
		if _, er := tx.PutEvent(ctx, changed); er != nil {
			return er
		}
		return abort
	}); !errors.Is(err, abort) {
		t.Fatal(err)
	}
	got, _, err := c.Event(ctx, e.ID)
	if err != nil || !reflect.DeepEqual(got, e) {
		t.Fatalf("replay changed event: %v", err)
	}
	if _, found, er := c.Event(ctx, "rollback"); er != nil || found {
		t.Fatalf("rolled-back event found=%v err=%v", found, er)
	}
	if n := count(t, c.r, "SELECT count(*) FROM col_identities"); n != 1 {
		t.Fatalf("rolled-back identity remains: %d", n)
	}
	if n := count(t, c.r, "SELECT count(*) FROM col_raw"); n != 1 {
		t.Fatalf("rolled-back raw remains: %d", n)
	}
	var version string
	if err = c.r.QueryRowContext(ctx, "SELECT value FROM meta WHERE key='schema_version'").Scan(&version); err != nil || version != "3" {
		t.Fatalf("legacy schema changed: %s %v", version, err)
	}
	var body []byte
	if err = c.r.QueryRowContext(ctx, "SELECT body FROM events WHERE event_id='legacy'").Scan(&body); err != nil || string(body) != "legacy-body" {
		t.Fatalf("legacy body changed: %q %v", body, err)
	}
}
