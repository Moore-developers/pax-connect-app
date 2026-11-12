package controller

import (
	"context"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
	"testing"
	"time"
)

func TestLatestMatchJSONRestartUnknownAndStableTie(t *testing.T) {
	h := newTrafficHarness(t)
	at := h.clock.Truncate(trafficBucket5m).Add(time.Second)
	fact := trafficFlow("session/epoch/b", func(f *config.FlowFact) { f.Rule = "final"; f.RoutingMatchedAt = at; f.StartedAt = at })
	h.append(at, true, config.Observation{Session: "s", Sequence: 1, At: at, Kind: "started", Flow: fact, Upload: 9})
	h.settle()
	h.restart()
	var saved aggregateBucket
	if err := h.store.Traffic.Update(context.Background(), func(tx *historystore.TrafficTx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	read, err := h.store.BeginRead(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Close() }()
	err = read.Rows(context.Background(), false, at.Truncate(trafficBucket5m), at.Add(trafficBucket5m), []string{"domain"}, func(row historystore.TrafficRow) error { var e error; saved, e = decodeTrafficRow(row); return e })
	if err != nil {
		t.Fatal(err)
	}
	got := latestRoutingMatch(saved.Bases, at.Add(-time.Second), at.Add(time.Minute))
	if got == nil || got.Identity != fact.Key || got.Rule != "final" {
		t.Fatalf("JSON/restart lost recorded decision: %v", got)
	}
	// A byte-only old connection does not acquire a new decision time from the write.
	later := at.Add(3 * time.Minute)
	pending := pendingBuckets([]pendingEntry{{at: later, fact: fact, up: 5}}, at.Add(-time.Second), later.Add(time.Second))
	if len(pending) != 0 { // actual pending count must use the same bytes as the observation
		for _, v := range pending {
			mergeAggregateBases(&saved, v.Bases)
		}
	}
	// Legacy facts at a later bucket make an earlier known result insufficient.
	legacy, err := decodeTrafficRow(historystore.TrafficRow{TrafficKey: historystore.TrafficKey{BucketMs: later.Truncate(trafficBucket5m).UnixMilli()}, RuleBasis: []byte(`{"legacy":{"Rule":"domain=old.test","Source":"Pax 包：old","Connections":1}}`)})
	if err != nil {
		t.Fatal(err)
	}
	merged := mergeRoutingBases(saved.Bases, legacy.Bases)
	if latestRoutingMatch(merged, at.Add(-time.Second), later.Add(trafficBucket5m)) != nil {
		t.Fatal("older known match escaped later unknown legacy range")
	}
	newest := *fact
	newest.Key = "another-session/epoch/z"
	newest.RoutingMatchedAt = later.Add(trafficBucket5m)
	newBasis := observedRuleBasis(newest, trafficWrite{starts: 1}, config.Observation{At: newest.RoutingMatchedAt})
	merged = mergeRoutingBases(merged, newBasis)
	if v := latestRoutingMatch(merged, at.Add(-time.Second), newest.RoutingMatchedAt.Add(time.Second)); v == nil || v.Identity != newest.Key {
		t.Fatal("new decision after unknown bound did not recover", v)
	}
	same := newest
	same.Key = "another-session/epoch/zz"
	tied := mergeRoutingBases(newBasis, observedRuleBasis(same, trafficWrite{failures: 1}, config.Observation{At: same.RoutingMatchedAt}))
	if v := latestRoutingMatch(tied, at, newest.RoutingMatchedAt.Add(time.Second)); v == nil || v.Identity != same.Key {
		t.Fatal("equal-time identity order unstable", v)
	}
	if latestRoutingMatch(tied, newest.RoutingMatchedAt.Add(time.Millisecond), newest.RoutingMatchedAt.Add(time.Second)) != nil {
		t.Fatal("selection outside chosen window leaked through later traffic")
	}
}
