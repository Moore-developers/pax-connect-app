package controller

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// Acceptance: SQL count/order/filter/page and complete returned rows equal the
// production projection for complete, from-cut, until-cut and route-cut groups.
func TestColumnLogWindowDifferential(t *testing.T) {
	ctx := context.Background()
	c := openColumnProjectionTest(t)
	batch := columnProjectionFixture()
	if err := c.Update(ctx, func(tx *historystore.ColumnsTx) error {
		for _, v := range batch.Connections {
			if e := tx.PutConnection(ctx, v); e != nil {
				return e
			}
		}
		for _, v := range batch.Events {
			if _, e := tx.PutEvent(ctx, v); e != nil {
				return e
			}
		}
		return projectColumnBatch(ctx, tx, batch)
	}); err != nil {
		t.Fatal(err)
	}
	base := batch.Events[0].At
	filters := []*pb.LogQuery{{IncludeDns: true}, {IncludeDns: false}, {IncludeDns: true, Level: "warning"}, {IncludeDns: true, Scope: "system"}, {IncludeDns: true, Scope: "access"}, {IncludeDns: true, Dimension: "application", Scope: "access"}, {IncludeDns: true, Quick: "problems"}, {IncludeDns: true, Quick: "blocked"}, {IncludeDns: true, Quick: "slow"}, {IncludeDns: true, Dimension: "domain", ObjectId: "example.test"}, {IncludeDns: true, ConnectionKey: batch.Connections[0].Fact.Key}}
	for _, criteria := range []*pb.LogCriteria{{Kinds: []string{"dns"}}, {Kinds: []string{"access"}}, {Levels: []string{"warning", "error"}}, {Application: "Browser"}, {Domain: "example.test", DomainMode: "suffix"}, {Domain: "www.example.test", DomainMode: "exact"}, {Domain: "exam", DomainMode: "contains"}, {TargetIp: "192.0.2.0/24"}, {Transports: []string{"tcp"}}, {Port: 443}, {Directions: []string{"proxy"}}, {Route: "route"}, {RulePackage: "Rules"}, {MinLatencyMs: 1}, {DnsAnswerIp: "192.0.2.0/24"}} {
		filters = append(filters, &pb.LogQuery{IncludeDns: true, Criteria: criteria})
	}
	filters = append(filters, &pb.LogQuery{IncludeDns: true, Criteria: &pb.LogCriteria{MinLatencyMs: math.MaxUint64}})
	var comparisons, pages int
	for _, view := range []string{"", "activity", "access", "problems", "operations"} {
		for wi, w := range []historystore.ColumnLogWindow{{From: base.Add(-time.Second), Until: base.Add(10 * time.Second)}, {From: base.Add(1500 * time.Millisecond), Until: base.Add(10 * time.Second)}, {From: base.Add(-time.Second), Until: base.Add(1500 * time.Millisecond)}, {From: base.Add(-time.Second), Until: base.Add(10 * time.Second), Route: "r"}, {From: time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), Until: time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)}} {
			w.View, w.Collection, w.Cutoff = view, "collecting", base.Add(-time.Hour)
			w.Apps = []config.RoutingApplication{{ID: "app", Name: "Installed Browser", Path: "/Applications/Browser", Bundle: true}}
			w.Classify = func(id, path string) (config.RoutingApplication, bool) { return userApplicationMatch(id, path, w.Apps) }
			var events []config.HistoryEvent
			for _, e := range batch.Events {
				if e.At.After(w.From) && !e.At.After(w.Until) && (w.Route == "" || e.Flow != nil && e.Flow.RouteID == w.Route) {
					events = append(events, e)
				}
			}
			// A retained model's group order is time descending, ID descending.
			sortLogEvents(events)
			summaries := map[string]config.LogConnectionSummary{}
			for _, s := range batch.Connections {
				summaries[s.Fact.Key] = s
			}
			r := &activityProjectionOracle{logs: events, summaries: summaries, until: w.Until, meta: historyMetadata{Collection: w.Collection}}
			if view == "activity" {
				if err := freezeActivityOracle(ctx, r); err != nil {
					t.Fatal(err)
				}
				r.userApplications = classifySharedLogSources(r.records, w.Apps)
			}
			snap, err := c.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			err = snap.WithRead(ctx, func(q historystore.ColumnQuery) error {
				sel, err := historystore.PrepareColumnLogs(ctx, q, w, func(ctx context.Context, s *historystore.ColumnLogSelection, key string) (historystore.ColumnGroup, error) {
					return projectColumnGroupStream(ctx, columnSelectionStream{s}, view, key, w.Until, w.Collection)
				})
				if err != nil {
					return err
				}
				if view == "activity" {
					apps, routes, rules, e := sel.Options(ctx)
					if e != nil {
						return e
					}
					got := &pb.LogOptions{Applications: apps, Routes: routes, RulePackages: rules}
					if !proto.Equal(got, logOptions(r.records)) {
						t.Fatal("options differ", got, logOptions(r.records))
					}
				}
				if view == "activity" {
					for _, dim := range []string{"application", "domain", "ip", "system"} {
						query := &pb.LogQuery{View: view, Dimension: dim, IncludeDns: true, Limit: 100, Scope: "system", ObjectId: "unknown"}
						for _, search := range []string{"", "example", "missing"} {
							query.Search = search
							want, e := summaryObjects(ctx, r, "token", query)
							if e != nil {
								return e
							}
							f := columnLogFilter(query)
							f.ObjectID = ""
							items, total, unknown, _, selected, e := sel.Objects(ctx, f, search, query.ObjectId, 0, 100)
							if e != nil {
								return e
							}
							got := &pb.LogObjectPage{ReadToken: "token", History: historyState(r.meta), TotalEvents: total, UnknownEvents: unknown}
							for _, v := range items {
								got.Items = append(got.Items, &pb.LogObject{Id: v.ID, Name: v.Name, Symbol: v.Symbol, Count: v.Count})
							}
							if selected != nil {
								got.SelectedObject = &pb.LogObject{Id: selected.ID, Name: selected.Name, Symbol: selected.Symbol, Count: selected.Count}
							}
							if !proto.Equal(got, want) {
								t.Fatalf("directory window=%d dimension=%s search=%s\ngot %s\nwant %s", wi, dim, search, got, want)
							}
						}
					}
				}
				for fi, filter := range filters {
					filter = proto.Clone(filter).(*pb.LogQuery)
					filter.View = view
					var want []proto.Message
					if view == "activity" {
						for _, p := range r.records {
							if r.recordMatches(p, filter) {
								want = append(want, r.materializeRecord(p))
							}
						}
					} else if view == "" {
						for _, e := range events {
							if filter.Level != "" && e.Level != filter.Level {
								continue
							}
							id, _ := logObject(e, filter.Dimension)
							if filter.ObjectId != "" && filter.ObjectId != "all" && id != filter.ObjectId {
								continue
							}
							want = append(want, eventProto(e))
						}
					} else {
						items, e := projectLogView(ctx, events, filter)
						if e != nil {
							return e
						}
						for _, p := range items {
							want = append(want, p)
						}
					}
					f := columnLogFilter(filter)
					n, e := sel.Count(ctx, f)
					if e != nil {
						return e
					}
					if int(n) != len(want) {
						t.Fatalf("view=%s window=%d filter=%d total=%d want=%d", view, wi, fi, n, len(want))
					}
					for offset := 0; offset < max(1, len(want)); offset += 2 {
						keys, e := sel.KeysPage(ctx, f, offset, 2)
						if e != nil {
							return e
						}
						for i, key := range keys {
							var got proto.Message
							if view == "" {
								e, _, er := sel.Event(ctx, key, false)
								if er != nil {
									return er
								}
								got = eventProto(e)
							} else {
								g, _, er := sel.Group(ctx, key)
								if er != nil {
									return er
								}
								e, _, er := sel.Event(ctx, g.EventID, false)
								if er != nil {
									return er
								}
								if view == "activity" {
									local := &historyRead{until: w.Until, meta: r.meta, columnLogRead: &columnLogReadState{window: w}}
									got = materializeColumnLogRecord(local, columnGroupRecord(g, e))
								} else {
									got = columnGroupProduct(g, e)
								}
							}
							if !proto.Equal(got, want[offset+i]) {
								t.Fatalf("view=%s window=%d filter=%d row=%d\ngot %s\nwant %s", view, wi, fi, offset+i, got, want[offset+i])
							}
							comparisons++
						}
						pages++
					}
				}
				return nil
			})
			_ = snap.Close()
			if err != nil {
				t.Fatal(view, wi, err)
			}
		}
	}
	t.Logf("COLUMN_LOG_DIFF cases=%d pages=%d full_proto_rows=%d differences=0", 5*5*len(filters), pages, comparisons)
}

func sortLogEvents(events []config.HistoryEvent) {
	// Keep the production history list's exact tie order, independent of seq.
	for i := 1; i < len(events); i++ {
		for j := i; j > 0; j-- {
			a, b := events[j], events[j-1]
			if !a.At.After(b.At) && (!a.At.Equal(b.At) || a.ID <= b.ID) {
				break
			}
			events[j], events[j-1] = events[j-1], events[j]
		}
	}
}

// Acceptance: one streaming group can consume a large event stream while its
// mutable reducer owns only scalar facts. The result equals the same production fold.
func TestColumnLogHotGroupReducer(t *testing.T) {
	ctx := context.Background()
	c := openColumnProjectionTest(t)
	at := time.Now().Add(-time.Hour)
	const total = 4096
	if err := c.Update(ctx, func(tx *historystore.ColumnsTx) error {
		for i := 0; i < total; i++ {
			f := config.FlowFact{Key: "hot", Domain: "example.test", StartedAt: at, Direction: "direct"}
			e := config.HistoryEvent{Sequence: uint64(i + 1), ID: fmt.Sprintf("hot-%08d", i), At: at.Add(time.Duration(i) * time.Microsecond), Level: "info", Source: "sing-box", Flow: &f}
			if _, err := tx.PutEvent(ctx, e); err != nil {
				return err
			}
			if err := tx.PutEventIndex(ctx, columnEventIndex(e)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	g, err := projectColumnGroup(ctx, c, "access", accessRecordID("hot"))
	if err != nil {
		t.Fatal(err)
	}
	if g.Count != total || g.Related != 1 {
		t.Fatal(g.Count, g.Related)
	}
}
