package controller

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

func columnProjectionFixture() historystore.ColumnImportBatch {
	at := time.Date(2026, 11, 14, 0, 0, 0, 123456789, time.UTC)
	f := config.FlowFact{Key: "s/1/a", ApplicationID: "app", Application: "Browser", Process: "/Applications/Browser", Domain: "www.example.test", IP: "192.0.2.1", Port: 443, Transport: "tcp", RouteID: "r", RouteName: "route", Direction: "proxy", StartedAt: at, Rule: "domain=example.test", RuleSource: "Pax 包：Rules"}
	ready := f
	ready.Phase = "ready"
	ready.EstablishmentState = "ready"
	ready.ResponseKnown = true
	closed := ready
	end := at.Add(3 * time.Second)
	closed.ClosedAt = &end
	closed.Phase = "closed"
	m := config.ConnectionMetrics{Upload: 123, Download: 456, BytesState: "complete", BytesFrom: at, AsOf: end}
	dns := config.FlowFact{Key: "s/1/dns", Purpose: "dns", Domain: "www.example.test", StartedAt: at, Transport: "udp", Port: 53, Direction: "dns", ResponseKnown: true, ResponseMS: 8, DNS: &config.DNSFact{QueryType: "A", Result: "success", Answers: []string{"192.0.2.2", "192.0.2.1", "192.0.2.2"}}}
	bad := f
	bad.Key = "s/1/b"
	bad.Failure = "dial_failed"
	bad.EstablishmentState = "failed"
	b := historystore.ColumnImportBatch{Connections: []config.LogConnectionSummary{{Fact: closed, Metrics: m, LastEventID: "close", LastLogAt: end, LastSequence: 3}, {Fact: dns, Metrics: config.ConnectionMetrics{AsOf: at.Add(time.Second)}, LastEventID: "dns", LastLogAt: at.Add(time.Second), LastSequence: 4}, {Fact: bad, LastEventID: "bad", LastLogAt: end, LastSequence: 5}}}
	b.Events = []config.HistoryEvent{
		{Sequence: 1, ID: "start", At: at, Kind: "started", Level: "info", Source: "sing-box", Flow: &f, Raw: []string{"not projected"}},
		{Sequence: 2, ID: "ready", At: at.Add(time.Second), Kind: "ready", Level: "warning", Source: "sing-box", Flow: &ready},
		{Sequence: 3, ID: "close", At: end, Kind: "closed", Level: "info", Source: "sing-box", Flow: &closed, Metrics: &m},
		{Sequence: 4, ID: "dns", At: at.Add(time.Second), Kind: "dns", Level: "info", Source: "sing-box", Flow: &dns},
		{Sequence: 5, ID: "bad", At: end, Kind: "failed", Level: "error", Source: "sing-box", Flow: &bad, Result: "failed"},
		{Sequence: 6, ID: "op", At: end.Add(time.Second), Kind: "operation", Level: "error", Source: "Pax", Title: "线路操作失败", OperationID: "op", OperationFrom: "old", OperationTo: "new", System: &config.SystemEvent{Type: "routes.test", Name: "线路检查", Result: "failed", ErrorCode: "error", Category: config.EventCategory{Key: "routes", Name: "线路", Symbol: "network"}, StartedAt: &at, Sections: []config.EventSection{{Kind: "facts", Fields: []config.EventField{{Label: "Reason", Value: "failure"}}}}}},
		{Sequence: 7, ID: "diagnostic", At: end, Kind: "diagnostic", Level: "warning", Source: "sing-box", Title: "诊断"},
	}
	return b
}

func openColumnProjectionTest(t *testing.T) *historystore.Columns {
	t.Helper()
	c, err := historystore.OpenColumns(context.Background(), newLogsTestDir(t), uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err = c.EnsureProjections(context.Background()); err != nil {
		t.Fatal(err)
	}
	return c
}

// Acceptance: complete persisted groups reconstruct the full production proto,
// preserving merged flow identity, DNS order, metrics and native system content.
func TestColumnProjectionsMatchProduction(t *testing.T) {
	ctx := context.Background()
	c := openColumnProjectionTest(t)
	b := columnProjectionFixture()
	if err := c.Update(ctx, func(tx *historystore.ColumnsTx) error {
		for _, s := range b.Connections {
			if e := tx.PutConnection(ctx, s); e != nil {
				return e
			}
		}
		for _, e := range b.Events {
			if _, er := tx.PutEvent(ctx, e); er != nil {
				return er
			}
		}
		return projectColumnBatch(ctx, tx, b)
	}); err != nil {
		t.Fatal(err)
	}
	checkColumnProjectionOracle(t, c)
	checkColumnMemberOracle(t, c)
	before, err := c.Members(ctx, "s/1/a")
	if err != nil || len(before) == 0 {
		t.Fatal(before, err)
	}
	b.Connections[0].Metrics.Upload = 999
	if err = c.Update(ctx, func(tx *historystore.ColumnsTx) error {
		if er := tx.PutConnection(ctx, b.Connections[0]); er != nil {
			return er
		}
		return projectColumnBatch(ctx, tx, historystore.ColumnImportBatch{Connections: b.Connections[:1]})
	}); err != nil {
		t.Fatal(err)
	}
	g, ok, err := c.Group(ctx, "activity", accessRecordID("s/1/a"))
	if err != nil || !ok || g.Metrics.Upload != 999 {
		t.Fatal(g, ok, err)
	}
	checkColumnProjectionOracle(t, c)
	checkColumnMemberOracle(t, c)
}

// Acceptance: a projection failure cannot leave facts or query keys committed.
func TestColumnProjectionFailureRollsBackFactBatch(t *testing.T) {
	ctx := context.Background()
	c := openColumnProjectionTest(t)
	e := config.HistoryEvent{Sequence: 1, ID: "rollback", At: time.Now(), Source: "Pax", Kind: "operation"}
	failure := errors.New("injected projection failure")
	err := c.Update(ctx, func(tx *historystore.ColumnsTx) error {
		if _, er := tx.PutEvent(ctx, e); er != nil {
			return er
		}
		if er := projectColumnBatch(ctx, tx, historystore.ColumnImportBatch{Events: []config.HistoryEvent{e}}); er != nil {
			return er
		}
		if er := tx.SetProjectionProgress(ctx, "failure", "rollback"); er != nil {
			return er
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if _, ok, er := c.Event(ctx, e.ID); er != nil || ok {
		t.Fatal("fact survived failed projection", ok, er)
	}
	keys, err := c.GroupKeys(ctx, "activity", "", 100)
	if err != nil || len(keys) != 0 {
		t.Fatal("index survived failed projection", keys, err)
	}
	if _, ok, er := c.Group(ctx, "activity", "event:rollback"); er != nil || ok {
		t.Fatal("group survived failed projection", ok, er)
	}
	if p, er := c.ProjectionProgress(ctx, "failure"); er != nil || p != "" {
		t.Fatal("checkpoint survived failed projection", p, er)
	}
}

// Acceptance: backfill from facts uses the production rules and is repeatable.
func TestColumnProjectionRebuildFromFacts(t *testing.T) {
	ctx := context.Background()
	c := openColumnProjectionTest(t)
	b := columnProjectionFixture()
	if err := c.Update(ctx, func(tx *historystore.ColumnsTx) error {
		for _, s := range b.Connections {
			if er := tx.PutConnection(ctx, s); er != nil {
				return er
			}
		}
		for _, e := range b.Events {
			if _, er := tx.PutEvent(ctx, e); er != nil {
				return er
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := rebuildColumnProjections(ctx, c); err != nil {
			t.Fatal(err)
		}
		checkColumnProjectionOracle(t, c)
	}
}

func checkColumnProjectionOracle(t *testing.T, c *historystore.Columns) {
	t.Helper()
	ctx := context.Background()
	expected := map[string]map[string][]string{}
	for _, view := range []string{"", "activity", "access", "problems", "operations"} {
		expected[view] = map[string][]string{}
	}
	if err := c.EachEvent(ctx, func(e config.HistoryEvent) error {
		for view, groups := range expected {
			key := logViewKey(e, view)
			if key != "" {
				groups[key] = append(groups[key], e.ID)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rawAfter := ""
	rawCount := 0
	for {
		keys, err := c.GroupKeys(ctx, "", rawAfter, 256)
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) == 0 {
			break
		}
		for _, key := range keys {
			if _, ok := expected[""][key]; !ok {
				t.Fatal("unexpected raw key", key)
			}
			delete(expected[""], key)
			rawCount++
		}
		rawAfter = keys[len(keys)-1]
	}
	if len(expected[""]) != 0 {
		t.Fatal("missing raw keys", len(expected[""]))
	}
	groups, rows := 0, 0
	for _, view := range []string{"activity", "access", "problems", "operations"} {
		after := ""
		for {
			keys, err := c.GroupKeys(ctx, view, after, 128)
			if err != nil {
				t.Fatal(err)
			}
			if len(keys) == 0 {
				break
			}
			for _, key := range keys {
				events, er := c.GroupEvents(ctx, view, key)
				if er != nil {
					t.Fatal(er)
				}
				wantIDs, exists := expected[view][key]
				if !exists {
					t.Fatal("unexpected group key", view, key)
				}
				gotIDs := make([]string, len(events))
				for i, event := range events {
					gotIDs[i] = event.ID
				}
				sort.Strings(wantIDs)
				sort.Strings(gotIDs)
				if !reflect.DeepEqual(wantIDs, gotIDs) {
					t.Fatal("group membership differs", view, key, len(wantIDs), len(gotIDs))
				}
				delete(expected[view], key)
				g, ok, er := c.Group(ctx, view, key)
				if er != nil || !ok {
					t.Fatal(view, key, ok, er)
				}
				if g.Flow != nil {
					fact := g.Flow.Fact
					pack, _ := config.TrafficRuleDescription(fact.Rule, fact.RuleSource)
					lat, known := config.FlowLatency(fact)
					if g.RulePackage != pack || g.Latency != lat || g.LatencyKnown != known {
						t.Fatalf("derived group filters differ view=%s key=%s", view, key)
					}
				} else if g.RulePackage != "" || g.Latency != 0 || g.LatencyKnown {
					t.Fatal("flowless group has flow filters", view, key)
				}
				e, ok, er := c.Event(ctx, g.EventID)
				if er != nil || !ok {
					t.Fatal(ok, er)
				}
				if view == "activity" {
					summaries := map[string]config.LogConnectionSummary{}
					for _, event := range events {
						if event.Flow == nil {
							continue
						}
						s, found, e2 := c.Connection(ctx, event.Flow.Key)
						if e2 != nil {
							t.Fatal(e2)
						}
						if found {
							summaries[s.Fact.Key] = s
						}
					}
					sort.Slice(events, func(i, j int) bool {
						if events[i].At.Equal(events[j].At) {
							return events[i].ID > events[j].ID
						}
						return events[i].At.After(events[j].At)
					})
					r := &activityProjectionOracle{logs: events, summaries: summaries, until: g.Until, meta: historyMetadata{Collection: "collecting"}}
					if er = freezeActivityOracle(ctx, r); er != nil {
						t.Fatal(er)
					}
					got := columnGroupRecord(g, e)
					if len(r.records) != 1 || !proto.Equal(got, r.records[0]) {
						t.Fatalf("activity full proto differs key=%s\ngot=%v\nwant=%v", key, got, r.records)
					}
				} else {
					want, e2 := projectLogView(ctx, events, &pb.LogQuery{View: view})
					if e2 != nil {
						t.Fatal(e2)
					}
					got := columnGroupProduct(g, e)
					if len(want) != 1 || !proto.Equal(got, want[0]) {
						t.Fatalf("%s full proto differs key=%s\ngot=%v\nwant=%v", view, key, got, want)
					}
				}
				groups++
				rows += len(events)
			}
			after = keys[len(keys)-1]
		}
		if len(expected[view]) != 0 {
			t.Fatal("missing group keys", view, len(expected[view]))
		}
	}
	t.Logf("COLUMN_GROUP_DIFF groups=%d input_members=%d raw_keys=%d full_proto_differences=0", groups, rows, rawCount)
}

func checkColumnMemberOracle(t *testing.T, c *historystore.Columns) {
	t.Helper()
	ctx := context.Background()
	connections, members := 0, 0
	if err := c.EachConnection(ctx, func(s config.LogConnectionSummary) error {
		// Oracle bypasses the new helper: use the production flow correction
		// and identity generator, then check every persisted directory field.
		var lastEvent config.HistoryEvent
		if s.LastEventID != "" {
			event, ok, err := c.Event(ctx, s.LastEventID)
			if err != nil {
				return err
			}
			if ok {
				lastEvent = event
			}
		}
		fact := retainedConnectionFlow(s, lastEvent, time.UnixMilli(1<<62)).Fact
		identities := aggregateIdentities(fact)
		identities = append(identities, aggregateIdentities(s.Fact)...)
		latency, known := config.FlowLatency(fact)
		seen := map[string]bool{}
		var want []historystore.ColumnMember
		for _, id := range identities {
			key := aggregateMemberKey(id)
			if seen[key] {
				continue
			}
			seen[key] = true
			want = append(want, historystore.ColumnMember{Dimension: id.Dimension, Object: id.Object, Name: id.Name, Kind: id.Kind, Route: id.Route, RouteName: id.RouteName, Direction: id.Direction, Search: id.Search, RulePackage: id.RulePackage, StartedAt: s.Fact.StartedAt, LatencyKnown: known, Latency: latency})
		}
		got, err := c.Members(ctx, s.Fact.Key)
		if err != nil {
			return err
		}
		less := func(v []historystore.ColumnMember) func(int, int) bool {
			return func(i, j int) bool {
				a, b := v[i], v[j]
				if a.Dimension != b.Dimension {
					return a.Dimension < b.Dimension
				}
				if a.Object != b.Object {
					return a.Object < b.Object
				}
				if a.Route != b.Route {
					return a.Route < b.Route
				}
				return a.Direction < b.Direction
			}
		}
		for i := range want {
			want[i].StartedAt = want[i].StartedAt.UTC()
		}
		for i := range got {
			got[i].StartedAt = got[i].StartedAt.UTC()
		}
		sort.Slice(want, less(want))
		sort.Slice(got, less(got))
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("connection directory differs key=%s\ngot=%#v\nwant=%#v", s.Fact.Key, got, want)
		}
		connections++
		members += len(want)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Logf("COLUMN_MEMBER_DIFF connections=%d members=%d differences=0", connections, members)
}
