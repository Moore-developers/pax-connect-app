package controller_test

import (
	"context"
	"os"
	"path/filepath"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/controller"
	"testing"
	"time"
)

func TestTrafficSourceTotalsAndDimensionCache(t *testing.T) {
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
	at := time.Now().UTC().Truncate(time.Second).Add(-5 * time.Second)
	if at.In(time.Local).Day() != time.Now().In(time.Local).Day() {
		t.Skip("fixture crosses midnight; dedicated window tests cover the boundary")
	}
	facts := []config.FlowFact{{Key: "scope/1/app", ApplicationID: "browser", Application: "Browser"}, {Key: "scope/1/system", Process: "/usr/libexec/trustd"}, {Key: "scope/1/unknown"}}
	var records []config.Observation
	for i, f := range facts {
		f.Domain = "api.example.test"
		f.Direction = "direct"
		f.RouteID = "direct"
		f.StartedAt = at
		end := at.Add(time.Second)
		f.ClosedAt = &end
		records = append(records, config.Observation{Session: "scope", Sequence: uint64(i + 1), Kind: "closed", At: end, Flow: &f, Upload: uint64((i + 1) * 10), Download: uint64((i + 1) * 100)})
	}
	if e = controller.FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "scope", Collecting: true, HighWater: 3, Records: records}); e != nil {
		t.Fatal(e)
	}
	// Source subtotals and the network total intentionally use different scopes.
	today, e := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: "today"})
	if e != nil || today.Upload != 10 || today.Download != 100 {
		t.Fatalf("identified today subtotal: up=%d down=%d err=%v", today.GetUpload(), today.GetDownload(), e)
	}
	for _, dim := range []string{"application", "domain", "application"} {
		p, e := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: dim, Window: "day"})
		if e != nil {
			t.Fatal(e)
		}
		want := uint64(10)
		if dim == "domain" {
			want = 60
		}
		var chart uint64
		for _, point := range p.Trend {
			chart += point.Upload
		}
		if p.Upload != want || chart != want {
			t.Fatalf("%s source/cache: total=%d chart=%d want=%d", dim, p.Upload, chart, want)
		}
	}
	network, e := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "network", Window: "today"})
	if e != nil || network.Upload != 60 || network.Download != 600 || len(network.Items) != 0 {
		t.Fatal("network total lost system or unknown bytes", network, e)
	}
}
