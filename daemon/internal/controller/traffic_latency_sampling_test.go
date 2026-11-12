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

func TestLatencySamplingCoversWholeWindowAndFrozenDetail(t *testing.T) {
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
	now := time.Now().Add(-time.Second)
	start := now.Add(-23 * time.Hour)
	var records []config.Observation
	for i := 0; i < 1024; i++ {
		at := start.Add(time.Duration(i) * time.Minute)
		closed := at.Add(time.Second)
		ms := uint64(35)
		if i == 100 {
			ms = 9999
		}
		if i == 800 {
			ms = 0
		}
		f := config.FlowFact{Key: fmt.Sprint("s/1/", i), ApplicationID: "a", Application: "a", Direction: "direct", StartedAt: at, ClosedAt: &closed, ResponseKnown: true, ResponseMS: ms}
		records = append(records, config.Observation{Session: "s", Sequence: uint64(i + 1), Kind: "closed", At: closed, Flow: &f})
	}
	if e = controller.FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "s", HighWater: 1024, Collecting: true, Records: records}); e != nil {
		t.Fatal(e)
	}
	p, e := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: "day"})
	if e != nil {
		t.Fatal(e)
	}
	r := p.Items[0]
	if !r.LatencySampled || len(r.LatencySamples) > 256 || r.ValidLatencyCount != 1024 {
		t.Fatal("budget or coverage", r)
	}
	first, last := r.LatencySamples[0], r.LatencySamples[len(r.LatencySamples)-1]
	if !first.At.AsTime().Equal(start) || !last.At.AsTime().Equal(start.Add(1023*time.Minute)) {
		t.Fatal("lost endpoints")
	}
	peak, zero := false, false
	for i, s := range r.LatencySamples {
		peak = peak || s.Milliseconds == 9999
		zero = zero || s.Milliseconds == 0
		if i > 0 && s.At.AsTime().Before(r.LatencySamples[i-1].At.AsTime()) {
			t.Fatal("non temporal")
		}
	}
	if !peak || !zero || r.LatestLatencyMs != 35 {
		t.Fatal("lost extrema or latest")
	}
	d, e := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{RowId: r.Id, ReadToken: p.ReadToken, Limit: 1})
	if e != nil || !proto.Equal(r, d.Summary) {
		t.Fatal("detail diverged", e)
	}
	d, e = c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{RowId: r.Id, ReadToken: p.ReadToken, Cursor: d.NextCursor, Limit: 1})
	if e != nil || !proto.Equal(r, d.Summary) {
		t.Fatal("pagination changed chart", e)
	}
}
