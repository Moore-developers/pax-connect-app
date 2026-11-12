package controller_test

import (
	"context"
	"fmt"
	"google.golang.org/protobuf/proto"
	"os"
	"path/filepath"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/controller"
	"testing"
	"time"
)

func TestClusteredLatencyRetainsEveryValidSample(t *testing.T) {
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
	defer func() { _ = controller.CloseHistoryFixture(c, ctx) }()
	now := time.Now().UTC().Truncate(time.Second)
	var records []config.Observation
	for i := 0; i < 162; i++ {
		at := now.Add(-time.Minute + time.Duration(i)*100*time.Millisecond)
		closed := at.Add(time.Second)
		f := config.FlowFact{Key: fmt.Sprintf("latency/1/%d", i), Purpose: "access", ApplicationID: "app", Application: "fixture", Domain: "fixture.test", IP: "192.0.2.1", Direction: "direct", RouteID: "direct", StartedAt: at, ClosedAt: &closed, ResponseKnown: true, ResponseMS: uint64(20 + i)}
		started := f
		started.ClosedAt, started.ResponseKnown = nil, false
		records = append(records,
			config.Observation{Session: "latency", Sequence: uint64(i*2 + 1), Kind: "started", At: at, Flow: &started},
			config.Observation{Session: "latency", Sequence: uint64(i*2 + 2), Kind: "closed", At: closed, Flow: &f})
	}
	if e = controller.FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "latency", Collecting: true, HighWater: uint64(len(records)), Records: records}); e != nil {
		t.Fatal(e)
	}
	page, e := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: "day"})
	if e != nil {
		t.Fatal(e)
	}
	if len(page.Items) != 1 {
		t.Fatal("row count", len(page.Items))
	}
	row := page.Items[0]
	detail, e := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: page.ReadToken, RowId: row.Id, Limit: 200})
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("connections=%d valid_responses=%d list_chart_points=%d detail_timing_records=%d", row.Connections, detail.Successful, len(row.LatencySamples), len(detail.Timings))
	if row.Connections != 162 || detail.Successful != 162 || len(row.LatencySamples) != 162 || !proto.Equal(row, detail.Summary) {
		t.Fatal("clustered responses lost full-window count or latency evidence")
	}
	if len(row.RecentResponses) != 20 || len(detail.RecentResponses) != 60 {
		t.Fatalf("recent response limits: list=%d detail=%d", len(row.RecentResponses), len(detail.RecentResponses))
	}
	for i, point := range detail.RecentResponses {
		index := 102 + i
		if point.ConnectionKey != fmt.Sprintf("latency/1/%d", index) || point.Milliseconds != uint64(20+index) || !point.StartedAt.AsTime().Equal(now.Add(-time.Minute+time.Duration(index)*100*time.Millisecond)) {
			t.Fatalf("detail sample %d is not the retained real response", i)
		}
	}
	for i, point := range row.RecentResponses {
		if !proto.Equal(point, detail.RecentResponses[40+i]) {
			t.Fatalf("list sample %d differs from detail tail", i)
		}
	}
}

func TestLatencyChartDoesNotDependOnTimingPage(t *testing.T) {
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
	defer func() { _ = controller.CloseHistoryFixture(c, ctx) }()
	now := time.Now().UTC().Truncate(time.Second)
	var records []config.Observation
	for i := 0; i < 418; i++ {
		at := now.Add(-time.Minute + time.Duration(i)*100*time.Millisecond)
		closed := at.Add(time.Second)
		f := config.FlowFact{Key: fmt.Sprintf("latency/1/%d", i), Purpose: "access", ApplicationID: "app", Application: "fixture", Domain: "fixture.test", IP: "192.0.2.1", Direction: "direct", RouteID: "direct", StartedAt: at, ClosedAt: &closed, ResponseKnown: i < 162, ResponseMS: uint64(20 + i)}
		records = append(records, config.Observation{Session: "latency", Sequence: uint64(i + 1), Kind: "closed", At: closed, Flow: &f})
	}
	if e = controller.FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "latency", Collecting: true, HighWater: 418, Records: records}); e != nil {
		t.Fatal(e)
	}
	page, e := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: "day"})
	if e != nil {
		t.Fatal(e)
	}
	if len(page.Items) != 1 {
		t.Fatal("row count", len(page.Items))
	}
	row := page.Items[0]
	detail, e := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: page.ReadToken, RowId: row.Id, Limit: 200})
	if e != nil {
		t.Fatal(e)
	}
	validDetail := 0
	for _, f := range detail.Timings {
		if f.ResponseKnown {
			validDetail++
		}
	}
	t.Logf("connections=%d valid_responses=%d detail_timing_records=%d detail_valid_points=%d", row.Connections, detail.Successful, len(detail.Timings), validDetail)
	if detail.Successful != 162 || validDetail != 162 || len(detail.Summary.LatencySamples) != 162 || !proto.Equal(row, detail.Summary) {
		t.Fatal("valid timings were discarded before filtering; detail cannot draw its available response evidence")
	}
}
