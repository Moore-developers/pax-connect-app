package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"testing"
	"time"
)

func TestTrafficDimensionReuseAndResponseLatency(t *testing.T) {
	ctx := context.Background()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(dir, 0700)
	c := New("test", "")
	if err := ConfigureHistoryFixture(t, c, ctx, dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = CloseHistoryFixture(c, ctx) }()
	now := time.Now().UTC().Truncate(time.Second)
	seq := uint64(0)
	for block := 0; block < 20; block++ {
		records := []config.Observation{}
		for i := 0; i < 100; i++ {
			index := block*100 + i
			f := config.FlowFact{Key: fmt.Sprintf("traffic/1/%d", index), ApplicationID: "app", Application: "测试应用", Domain: fmt.Sprintf("%d.example.test", index%100), IP: "192.0.2.1", RouteID: "direct", RouteName: "直连", Direction: "direct", Rule: "rule_set=geosite-cn", StartedAt: now.Add(-10 * time.Second), ResponseKnown: index%2 == 0, ResponseMS: uint64(20 + index%60), EstablishmentState: "ready", EstablishmentMS: 1}
			seq++
			records = append(records, config.Observation{Session: "traffic", Sequence: seq, At: now.Add(-time.Second), Kind: "started", Flow: &f, Upload: 10, Download: 100})
		}
		if err := FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "traffic", HighWater: seq, Collecting: true, Records: records}); err != nil {
			t.Fatal(err)
		}
	}
	// The second query reuses the first one's frozen aggregate only inside the same wall-clock
	// second (the cutoff is truncated to it). Under -race each query takes hundreds of milliseconds,
	// so a pair that happens to straddle a second boundary is not a valid sample: start the pair just
	// after a boundary and, if it still straddled one, measure it again. The assertions are unchanged.
	var (
		first, second               *pb.TrafficPage
		base                        string
		firstElapsed, secondElapsed time.Duration
	)
	for attempt := 0; ; attempt++ {
		if attempt > 0 { // wait for the next boundary so the pair starts at the head of a second
			time.Sleep(time.Second - time.Duration(time.Now().Nanosecond()) + 10*time.Millisecond)
		}
		boundary := time.Now().Truncate(time.Second)
		started := time.Now()
		var err error
		first, err = c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: "day"})
		firstElapsed = time.Since(started)
		if err != nil {
			t.Fatal(err)
		}
		if len(first.Items) != 1 {
			t.Fatalf("application rows=%d, want 1", len(first.Items))
		}
		row := first.Items[0]
		if !row.LatencyKnown || row.SlowestLatencyMs < 20 {
			t.Fatalf("response latency: known=%t slowest=%d, want known and >=20ms", row.LatencyKnown, row.SlowestLatencyMs)
		}
		if len(row.LatencySamples) == 0 {
			t.Fatal("application row has no response samples")
		}
		if row.Basis != "中国域名" {
			t.Fatalf("geosite-cn basis=%q, want 中国域名", row.Basis)
		}
		base = first.ReadToken
		started = time.Now()
		second, err = c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "domain", Window: "day"})
		secondElapsed = time.Since(started)
		if !time.Now().Truncate(time.Second).Equal(boundary) && attempt < 4 {
			continue // the pair straddled a second boundary: not a valid sample
		}
		if err != nil || first.Upload != second.Upload || first.Download != second.Download || base == second.ReadToken || !first.AsOf.AsTime().Equal(second.AsOf.AsTime()) {
			t.Fatal("dimension must retain values without sharing filtered trend cache", err)
		}
		break
	}
	t.Logf("2000 flows first=%s dimension=%s", firstElapsed, secondElapsed)
	third, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "ip", Window: "day"})
	if err != nil || third.Connections != 2000 {
		t.Fatal(third, err)
	}
	detail, err := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: first.ReadToken, RowId: first.Items[0].Id})
	if err != nil || detail.Successful != 1000 || detail.Unmeasurable != 1000 || len(detail.Basis) != 1 || detail.Basis[0].Target == "" || detail.Basis[0].RouteName != "直连" {
		t.Fatal(detail, err)
	}
	var sample *pb.FlowRecord
	for _, f := range detail.Items {
		if f.ResponseKnown {
			sample = f
			break
		}
	}
	if sample == nil {
		t.Fatal("no response fixture")
	}
	one, err := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: first.ReadToken, ConnectionKey: sample.Key})
	if err != nil || !one.Summary.LatencyKnown || one.Summary.SlowestLatencyMs != sample.ResponseMs {
		t.Fatal("connection jump lost response latency", one, err)
	}

}

func TestTrafficBaseExpiresWithoutAnotherQuery(t *testing.T) {
	now := time.Now()
	cache := newReadTokenCache()
	cache.reads["expired"] = &historyRead{kind: "traffic", at: now.Add(-6 * time.Minute), timeUsed: now, aggregate: true, rows: []*pb.TrafficRow{{Name: "private.example"}}}
	if _, err := cache.take("expired", "traffic"); err == nil || cache.reads["expired"] != nil {
		t.Fatal("expired facts retained")
	}
	cache.reads["traffic"] = &historyRead{kind: "traffic", at: now, timeUsed: now, aggregate: true}
	cache.reads["logs"] = &historyRead{kind: "logs", at: now, timeUsed: now}
	cache.invalidate("traffic")
	if cache.reads["traffic"] != nil {
		t.Fatal("deleted traffic retained")
	}
	if cache.reads["logs"] == nil {
		t.Fatal("traffic invalidation removed independent log snapshot")
	}
}

func TestTrafficLatestLatencyIsNotMaximum(t *testing.T) {
	ctx := context.Background()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(dir, 0700)
	c := New("test", "")
	if err := ConfigureHistoryFixture(t, c, ctx, dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = CloseHistoryFixture(c, ctx) }()
	now := time.Now().UTC().Truncate(time.Second)
	facts := []config.FlowFact{{Key: "latest/1/old", Domain: "example.test", RouteID: "direct", Direction: "direct", StartedAt: now.Add(-10 * time.Second), ResponseKnown: true, ResponseMS: 240}, {Key: "latest/1/new", Domain: "example.test", RouteID: "direct", Direction: "direct", StartedAt: now.Add(-3 * time.Second), ResponseKnown: true, ResponseMS: 18}}
	records := []config.Observation{}
	for i := range facts {
		records = append(records, config.Observation{Session: "latest", Sequence: uint64(i + 1), At: now.Add(-time.Second), Kind: "sample", Flow: &facts[i], Download: 100})
	}
	if err := FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "latest", Collecting: true, HighWater: 2, Records: records}); err != nil {
		t.Fatal(err)
	}
	page, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "domain", Window: "day"})
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	row := page.Items[0]
	if row.LatestLatencyMs != 18 || row.SlowestLatencyMs != 240 || !row.LatestLatencyAt.AsTime().Equal(facts[1].StartedAt) {
		t.Fatal("latest response replaced by maximum", row)
	}
	detail, err := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: page.ReadToken, RowId: row.Id})
	if err != nil || detail.Summary.LatestLatencyMs != 18 {
		t.Fatal(detail, err)
	}
}
