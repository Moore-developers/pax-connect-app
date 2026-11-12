package controller_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/controller"
	"testing"
	"time"
)

func TestCalendarAggregateUsesDailyBoundaryAndMergedQuantiles(t *testing.T) {
	ctx := context.Background()
	dir, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	c := controller.New("test", "test")
	if e = controller.ConfigureHistoryFixture(t, c, ctx, dir, uint32(os.Geteuid())); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := controller.CloseHistoryFixture(c, ctx); e != nil {
			t.Error(e)
		}
	}()
	now := time.Now()
	local := now.In(time.Local)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	sequence := uint64(0)
	for i, at := range []time.Time{today.Add(-time.Hour), today.Add(-2 * time.Hour), today.Add(-3 * time.Hour)} {
		ms := uint64(100)
		if i == 2 {
			ms = 1000
		}
		f := config.FlowFact{Key: fmt.Sprintf("quantiles/1/%d", i), StartedAt: at, ApplicationID: "fixture.app", Application: "Fixture", RouteID: "a", Direction: "proxy", ResponseKnown: true, ResponseMS: ms, Rule: "final", RuleSource: "内核规则"}
		sequence++
		if e = controller.FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "quantiles", Collecting: true, Records: []config.Observation{{Session: "quantiles", Sequence: sequence, At: at, Kind: "started", Flow: &f, Download: 10}}}); e != nil {
			t.Fatal(e)
		}
	}
	page, e := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: "week"})
	if e != nil || len(page.Items) != 1 {
		t.Fatal(e)
	}
	row := page.Items[0]
	if row.Basis != "无" || len(row.Bases) != 1 || row.Bases[0].Connections != 3 {
		t.Fatalf("recorded routing basis lost: %q entries=%d", row.Basis, len(row.Bases))
	}
	for _, sample := range row.LatencySamples {
		if sample.Known && sample.At.AsTime().Equal(today.Add(-2*time.Hour)) && sample.Milliseconds != 100 {
			t.Fatalf("bucket shows quantile instead of latest: %d", sample.Milliseconds)
		}
	}
	if row.P50Ms < 100 || row.P50Ms > 125 || row.P95Ms != 1000 || row.ValidLatencyCount != 3 {
		t.Fatalf("merged quantiles wrong: p50=%d p95=%d count=%d", row.P50Ms, row.P95Ms, row.ValidLatencyCount)
	}
	if !page.From.AsTime().Equal(today.AddDate(0, 0, -6)) {
		t.Fatal("week is not six midnights plus today")
	}
	if len(row.LatencySamples) > 128 || len(page.Trend) > 128 {
		t.Fatal("unbounded bucket chart")
	}
	if !page.History.Partial || page.History.AggregateCoverageStart == nil {
		t.Fatal("backfill coverage falsely complete")
	}
	route, e := c.QueryRouteUsage(ctx, &pb.RouteHistoryRequest{RouteId: "a", Window: "week"})
	if e != nil || route.Download != 30 || route.TodayDownload != 0 {
		t.Fatalf("route range total incorrect: %v", e)
	}
}
