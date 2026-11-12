package controller

import (
	"context"
	"fmt"
	"google.golang.org/protobuf/types/known/timestamppb"
	"os"
	"path/filepath"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
	"testing"
	"time"
)

func TestHistoryFixDNSTodayDirection(t *testing.T) {
	h := newTrafficHarness(t)
	c := New("test", "test")
	c.traffic = h.tr
	c.history = &historyOwner{}
	now := SetHistoryFixtureClock(c)
	h.clock = now
	f := config.FlowFact{Key: "s/dns/a", StartedAt: now.Add(-20 * time.Minute), Purpose: "dns", Domain: "fixture.invalid", RouteID: "direct", Direction: "direct"}
	h.seq = 1
	h.append(now, true, config.Observation{Session: "s", Sequence: 1, Kind: "started", At: now.Add(-20 * time.Minute), Flow: &f})
	h.settle()
	for _, win := range []string{"today", "week", "month"} {
		a, e := c.QueryTraffic(context.Background(), &pb.TrafficQuery{Dimension: "system", Window: win, Direction: "all"})
		if e != nil {
			t.Fatal(e)
		}
		p, e := c.QueryTraffic(context.Background(), &pb.TrafficQuery{Dimension: "system", Window: win, Direction: "proxy"})
		if e != nil {
			t.Fatal(e)
		}
		t.Logf("%s: all=%v proxy=%v", win, a.GetDnsSummary() != nil, p.GetDnsSummary() != nil)
		if a.DnsSummary == nil || p.DnsSummary == nil || a.DnsSummary.Connections != p.DnsSummary.Connections {
			t.Errorf("%s lost DNS summary under direction filter", win)
		}
	}
}

func TestHistoryFixJournalGapSurvivesCrashAfterQuarantine(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	now := time.Now().UTC().Truncate(time.Hour)
	for i := 0; i < 2; i++ {
		at := now.Add(time.Duration(i-2) * time.Hour)
		f := trafficFlow(fmt.Sprintf("s/%d", i), func(f *config.FlowFact) {})
		o := config.Observation{Session: "s", Sequence: uint64(i + 1), At: at, Kind: "started", Flow: f, Upload: 10}
		if err := h.journal.Append(ctx, at, []historystore.Observation{journalRecord(o)}, historystore.Cursor{Session: "s", Seq: o.Sequence}); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.journal.Close(); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(h.dir, "journal", now.Add(-2*time.Hour).Format("2006010215")+".db")
	if err := os.WriteFile(bad, []byte("bad journal"), 0600); err != nil {
		t.Fatal(err)
	}
	reader, err := historystore.OpenJournal(h.dir, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	_, err = reader.ReadAfter(ctx, historystore.Cursor{}, 10)
	if err == nil {
		t.Fatal("expected bad journal")
	}
	_ = reader.Close()
	consumers := newJournalConsumers()
	consumers.reportDamage(err)
	owner, err := historystore.OpenJournal(h.dir, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	in := newHistoryIngest(owner, nil)
	in.consumers = consumers
	in.started = true
	in.cursor = historystore.Cursor{Session: "s", Seq: 2}
	in.repairJournal(ctx)
	losses, e := owner.PendingLosses(ctx)
	if e != nil || len(losses) != 1 {
		t.Fatal("expected durable pending gap", e)
	}
	// Simulate termination after quarantine and before the next journal append.
	_ = owner.Close()
	restarted, err := historystore.OpenJournal(h.dir, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restarted.Close() }()
	if _, err = restarted.ReadAfter(ctx, historystore.Cursor{}, 10); err != historystore.ErrJournalRecoveryPending {
		t.Fatal("reader bypassed durable gap", err)
	}
	resumed := newHistoryIngest(restarted, nil)
	resumed.started = true
	resumed.cursor = in.cursor
	if err = resumed.flushJournalLosses(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := restarted.ReadAfter(ctx, historystore.Cursor{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	var gaps int
	for _, r := range rows {
		if r.Kind == journalGapKind {
			gaps++
		}
	}
	t.Logf("restart rows=%d persisted_gaps=%d", len(rows), gaps)
	if gaps == 0 {
		t.Fatal("quarantined hour has no durable gap after restart")
	}
}

func TestHistoryFixIdentityArrivesDuringBacklogBeforeLaterCommit(t *testing.T) {
	h := newTrafficHarness(t)
	unknown := &config.FlowFact{Key: "s/late", StartedAt: trafficEpoch, Direction: "unknown"}
	records := make([]config.Observation, 0, trafficBatchRecords+1)
	records = append(records, h.obs(time.Second, "started", unknown, 1, 0))
	for range trafficBatchRecords {
		records = append(records, h.obs(time.Second, "sample", unknown, 1, 0))
	}
	h.append(h.clock, true, records...)
	if got := h.tr.step(context.Background()); got != 0 {
		t.Fatalf("backlog did not continue: %v", got)
	}
	up, _, _, _ := h.counters("unknown")
	if up != int64(trafficBatchRecords) {
		t.Fatalf("first committed =%d", up)
	}
	known := *unknown
	known.ApplicationID = "fixture.app"
	known.Application = "Fixture"
	known.Direction = "proxy"
	known.RouteID = "r"
	h.append(h.clock, true, h.obs(2*time.Second, "sample", &known, 1, 0))
	h.tr.step(context.Background())
	unknownUp, _, _, _ := h.counters("unknown")
	knownUp, _, _, _ := h.counters("application")
	t.Logf("after second commit: unknown_up=%d known_up=%d", unknownUp, knownUp)
	if unknownUp != int64(trafficBatchRecords) || knownUp != 2 {
		t.Fatalf("later uncommitted identity not used by second commit")
	}
}

func TestHistoryFixBoundaryLegacyStillMarkedGap(t *testing.T) {
	ctx := context.Background()
	dir := newLogsTestDir(t)
	now := time.Now()
	at := now.Add(-time.Hour).Truncate(5 * time.Minute)

	store, _, err := historystore.OpenTraffic(ctx, filepath.Join(dir, "history"), uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	c := New("test", "test")
	if err := c.ConfigureHistory(ctx, dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseHistory(ctx) }()
	tr, err := newHistoryTraffic(ctx, store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.traffic = tr
	if err = store.Update(ctx, func(tx *historystore.TrafficTx) error {
		if err := tx.PutTraffic5m(ctx, historystore.TrafficRow{TrafficKey: historystore.TrafficKey{BucketMs: at.UnixMilli(), Dimension: "route", Object: "r", Route: "r", Direction: "proxy"}, SourceKind: "app", UpBytes: 10, DownBytes: 90}); err != nil {
			return err
		}
		if err := tx.SetMeta(ctx, "legacy_route_imported", "1"); err != nil {
			return err
		}
		return tx.SetMeta(ctx, trafficMetaCoverageFrom, fmt.Sprint(at.Add(time.Minute).UnixMilli()))
	}); err != nil {
		t.Fatal(err)
	}
	result, err := c.QueryRouteUsage(ctx, &pb.RouteHistoryRequest{RouteId: "r", From: timestamppb.New(at), Until: timestamppb.New(at.Add(5 * time.Minute))})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("buckets=%d", len(result.Items))
	}
	t.Logf("up=%d down=%d gap=%v partial=%v", result.Items[0].Upload, result.Items[0].Download, result.Items[0].Gap, result.Items[0].Partial)
	if !result.Items[0].Gap {
		t.Fatal("legacy boundary data claimed full gap-free evidence")
	}
	rows := readTrafficTable(t, store, "traffic_5m")
	if len(rows) != 1 || rows[0].SourceKind != "app" {
		t.Fatal("gap marker rewrote attribution", rows)
	}

}
