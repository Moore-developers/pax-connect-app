package controller

import (
	"context"
	"fmt"
	"google.golang.org/protobuf/proto"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

func TestAggregateDetailRetainsFrozenConnectionPages(t *testing.T) {
	ctx := context.Background()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	c, _ := routingController(t)
	if err = ConfigureHistoryFixture(t, c, ctx, dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if e := CloseHistoryFixture(c, ctx); e != nil {
			t.Error(e)
		}
	}()
	at := time.Now().Add(-time.Minute)
	for i, key := range []string{"one", "two"} {
		f := config.FlowFact{Key: key, StartedAt: at.Add(time.Duration(i) * time.Second), ApplicationID: "fixture.app", Application: "Fixture", Domain: "example.test", Direction: "direct", RouteID: "direct", Rule: "final"}
		if err = FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "fixture", Collecting: true, Records: []config.Observation{{Session: "fixture", Sequence: uint64(i + 1), At: f.StartedAt, Kind: "started", Flow: &f}}}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "domain", Window: "today"})
	if err != nil || len(page.GetItems()) != 1 {
		t.Fatalf("page: %v", err)
	}
	query := &pb.TrafficDetailRequest{ReadToken: page.ReadToken, RowId: page.Items[0].Id, Limit: 1}
	first, err := c.QueryTrafficDetail(ctx, query)
	if err != nil || len(first.GetItems()) != 1 || first.GetNextCursor() == "" {
		t.Fatalf("retained connections unavailable: items=%d cursor=%q err=%v", len(first.GetItems()), first.GetNextCursor(), err)
	}
	f := config.FlowFact{Key: "three", StartedAt: time.Now(), ApplicationID: "fixture.app", Application: "Fixture", Domain: "example.test", Direction: "direct", RouteID: "direct"}
	if err = FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "fixture", Collecting: true, Records: []config.Observation{{Session: "fixture", Sequence: 3, At: f.StartedAt, Kind: "started", Flow: &f}}}); err != nil {
		t.Fatal(err)
	}
	query.Cursor = first.NextCursor
	next, err := c.QueryTrafficDetail(ctx, query)
	if err != nil || len(next.GetItems()) != 1 || next.GetNextCursor() != "" || next.Items[0].Key == first.Items[0].Key || next.Summary.Connections != 2 {
		t.Fatalf("frozen pagination changed: %v", err)
	}
}

// Long windows retain their aggregate scope while paging the still-saved details.
func TestAggregateLongWindowsExposeRetainedConnectionPages(t *testing.T) {
	c, _ := newTrafficRPCFixture(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Minute)
	var records []config.Observation
	for i := 0; i < 25; i++ {
		f := config.FlowFact{Key: fmt.Sprintf("long/%02d", i), StartedAt: at.Add(time.Duration(i) * time.Second), Domain: "example.test", Direction: "direct", RouteID: "direct"}
		records = append(records, config.Observation{Session: "long", Sequence: uint64(i + 1), Kind: "started", At: f.StartedAt, Flow: &f})
	}
	if e := FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "long", Records: records}); e != nil {
		t.Fatal(e)
	}
	for _, window := range []string{"week", "month"} {
		page, e := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "domain", Window: window})
		if e != nil || len(page.GetItems()) != 1 {
			t.Fatalf("%s page: rows=%d err=%v", window, len(page.GetItems()), e)
		}
		query := &pb.TrafficDetailRequest{ReadToken: page.ReadToken, RowId: page.Items[0].Id}
		detail, e := c.QueryTrafficDetail(ctx, query)
		if e != nil || len(detail.GetItems()) != 20 || detail.GetNextCursor() == "" || !detail.GetAggregateOnly() || detail.GetRetainedConnectionCount() != 25 || detail.GetSummary().GetConnections() != 25 {
			t.Fatalf("%s first page: items=%d retained=%d cursor=%q err=%v", window, len(detail.GetItems()), detail.GetRetainedConnectionCount(), detail.GetNextCursor(), e)
		}
		if detail.Items[0].Key != "long/24" || detail.Items[19].Key != "long/05" {
			t.Fatalf("%s first page not newest-first", window)
		}
		query.Cursor = detail.NextCursor
		next, e := c.QueryTrafficDetail(ctx, query)
		if e != nil || len(next.GetItems()) != 5 || next.GetNextCursor() != "" || next.GetRetainedConnectionCount() != 25 || !proto.Equal(detail.Summary, next.GetSummary()) {
			t.Fatalf("%s next page: items=%d retained=%d cursor=%q err=%v", window, len(next.GetItems()), next.GetRetainedConnectionCount(), next.GetNextCursor(), e)
		}
		if next.Items[0].Key != "long/04" || next.Items[4].Key != "long/00" {
			t.Fatalf("%s remaining page duplicated or lost members", window)
		}
	}
}
