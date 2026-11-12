package controller

import (
	"context"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

// liveFixture is one batch that exercises every exclusion of the old rule: closed,
// state-unknown and DNS control flows are not active connections.
func liveFixture() config.ObservationBatch {
	closedAt := testEpoch.Add(time.Second)
	active := flow("active")
	other := flow("other")
	done := flow("done")
	unknown := flow("unknown")
	unknown.StateUnknown = true
	dns := flow("dns")
	dns.Purpose = "dns"
	closed := *done
	closed.ClosedAt = &closedAt
	records := []config.Observation{
		{Kind: "started", Flow: active}, {Kind: "started", Flow: other}, {Kind: "started", Flow: done},
		{Kind: "started", Flow: unknown}, {Kind: "started", Flow: dns}, {Kind: "closed", Flow: &closed},
	}
	for i := range records {
		records[i].Session, records[i].Sequence, records[i].At = "fixture", uint64(i+1), testEpoch
	}
	return config.ObservationBatch{Session: "fixture", HighWater: uint64(len(records)), Collecting: true, Records: records}
}

func TestLiveActiveCountMatchesHistoryTodayRules(t *testing.T) {
	batch := liveFixture()
	old := &pb.TrafficSnapshot{}
	readHistoryGoldenPB(t, "live/history.pb", old)
	live := newLiveState()
	live.apply(config.ObservationBatch{Session: "fixture", Collecting: true, ResetToken: "t", ResetRequired: true, BaselineComplete: true}, testEpoch)
	live.apply(batch, testEpoch)
	snap := live.Snapshot(testEpoch)
	if old.ActiveConnections != 2 || uint64(snap.Active) != old.ActiveConnections || snap.Known != old.ActiveConnectionsKnown {
		t.Fatalf("live %+v disagrees with historyToday active=%d known=%v", snap, old.ActiveConnections, old.ActiveConnectionsKnown)
	}
	// Not collecting: the count is unknown, as the old Collection state said.
	live.apply(config.ObservationBatch{Session: "fixture", Collecting: false}, testEpoch)
	if live.Snapshot(testEpoch).Known {
		t.Fatal("count must be unknown when the core is not collecting")
	}
}

func TestWatchActiveConnectionsComeFromLiveStateWhenIngestRuns(t *testing.T) {
	c, _ := newTrafficRPCFixture(t)
	live := newLiveState()
	live.apply(config.ObservationBatch{Session: "other", Collecting: true, ResetToken: "t", ResetRequired: true, BaselineComplete: true, Baseline: []config.FlowFact{*flow("1"), *flow("2"), *flow("3")}}, time.Now())
	c.liveView.Store(live)
	out, err := c.trafficToday(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	expected := &pb.TrafficSnapshot{}
	readHistoryGoldenPB(t, "live/watch.pb", expected)
	if out.ActiveConnections != expected.ActiveConnections || out.ActiveConnectionsKnown != expected.ActiveConnectionsKnown {
		t.Fatalf("Watch changed legacy active claim: %v / %v", out, expected)
	}

	if out.ActiveConnections != 3 || !out.ActiveConnectionsKnown {
		t.Fatalf("Watch active connections %d known=%v, want the live 3", out.ActiveConnections, out.ActiveConnectionsKnown)
	}
}

func TestLiveWindowHasTwelveFiveSecondPoints(t *testing.T) {
	live := newLiveState()
	now := testEpoch.Add(63 * time.Second) // the window covers the 12 buckets ending at 5 s .. 60 s
	f := flow("a")
	var records []config.Observation
	for i, at := range []time.Duration{5 * time.Second, 10 * time.Second, 10 * time.Second, 60 * time.Second, 2 * time.Second} {
		records = append(records, config.Observation{Session: "s", Sequence: uint64(i + 1), At: testEpoch.Add(at), Kind: "sample", Flow: f, Upload: uint64(i + 1), Download: 10 * uint64(i+1)})
	}
	live.apply(config.ObservationBatch{Session: "s", Collecting: true, Records: records}, now)
	w := live.Snapshot(now).Window
	if len(w) != 12 {
		t.Fatalf("window has %d points", len(w))
	}
	// Last point is the bucket ending at 60 s; the first is the one ending at 5 s, which also
	// takes the 2 s sample (1 + 5 upload).
	if w[11] != (livePoint{Upload: 4, Download: 40}) {
		t.Fatalf("newest point %+v", w[11])
	}
	if w[0] != (livePoint{Upload: 6, Download: 60}) || w[1] != (livePoint{Upload: 5, Download: 50}) {
		t.Fatalf("oldest points %+v %+v", w[0], w[1])
	}
	// A minute later everything has aged out and the window is empty.
	later := now.Add(2 * time.Minute)
	live.apply(config.ObservationBatch{Session: "s", Collecting: true}, later)
	for i, p := range live.Snapshot(later).Window {
		if p != (livePoint{}) {
			t.Fatalf("point %d kept after expiry: %+v", i, p)
		}
	}
}

func TestLiveWatermarkSkipsReplayedRecordsAndKeepsCumulativeBytes(t *testing.T) {
	live := newLiveState()
	a := flow("a")
	batch := config.ObservationBatch{Session: "s", Collecting: true, Records: []config.Observation{
		{Session: "s", Sequence: 1, At: testEpoch, Kind: "started", Flow: a},
		{Session: "s", Sequence: 2, At: testEpoch.Add(5 * time.Second), Kind: "sample", Flow: a, Upload: 7, Download: 70},
	}}
	live.apply(batch, testEpoch)
	live.apply(batch, testEpoch) // re-pulled after a journal failure
	if f := live.flows["a"]; f.Upload != 7 || f.Download != 70 {
		t.Fatalf("replay double counted: %+v", f)
	}
	batch.Records = append(batch.Records, config.Observation{Session: "s", Sequence: 3, At: testEpoch.Add(10 * time.Second), Kind: "sample", Flow: a, Upload: 1, Download: 1})
	live.apply(batch, testEpoch)
	if f := live.flows["a"]; f.Upload != 8 || f.Download != 71 {
		t.Fatalf("only the new record may count: %+v", f)
	}
}
