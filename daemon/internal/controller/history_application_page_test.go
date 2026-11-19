package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

func TestApplicationWorkbenchPagingMatchesLegacySnapshot(t *testing.T) {
	ctx := context.Background()
	c, pipeline := newLogsTestController(t, newLogsTestDir(t))
	app := config.RoutingApplication{ID: "fixture.browser", Name: "Browser", Path: "/Applications/Fixture.app", Bundle: true}
	c.history.applicationInventory = &historyApplicationInventory{At: time.Now(), Apps: []config.RoutingApplication{app}}
	at := time.Now().Add(-time.Minute).Truncate(time.Second)
	batch := config.ObservationBatch{Session: "app-page", Collecting: true}
	for i := 0; i < 9; i++ {
		f := &config.FlowFact{
			Key: fmt.Sprintf("app-page/1/%d", i), ApplicationID: app.ID,
			Process: app.Path + "/Contents/MacOS/Fixture", StartedAt: at, Domain: "target.example",
		}
		if i%2 == 1 {
			f.ApplicationID = ""
			f.Process = app.Path + "/Contents/Helpers/Fixture Helper"
		}
		if i == 8 {
			f.ApplicationID, f.Process = "other", "/tmp/unrelated"
		}
		batch.HighWater++
		batch.Records = append(batch.Records, config.Observation{
			Session: batch.Session, Sequence: batch.HighWater, At: at, Kind: "started", Flow: f,
		})
	}
	pipeline.observe(batch)
	read := func(q *pb.LogQuery) *pb.LogPage {
		t.Helper()
		page, err := c.QueryLogs(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	q := &pb.LogQuery{
		View: "activity", Scope: "access", Dimension: "application", ObjectId: app.ID, Limit: 3,
		From: timestamppb.New(at.Add(-time.Second)), Until: timestamppb.New(at.Add(time.Second)),
		ApplicationWorkbenchPage: true,
	}
	first := read(q)
	q.ReadToken = first.ReadToken
	// A newly committed record sorts ahead of the frozen first page.
	f := &config.FlowFact{
		Key: "app-page/1/new", ApplicationID: app.ID,
		Process: app.Path + "/Contents/MacOS/Fixture", StartedAt: at.Add(500 * time.Millisecond),
	}
	batch.HighWater++
	batch.Records = []config.Observation{{
		Session: batch.Session, Sequence: batch.HighWater, At: f.StartedAt, Kind: "started", Flow: f,
	}}
	pipeline.observe(batch)
	legacy := proto.Clone(q).(*pb.LogQuery)
	legacy.ApplicationWorkbenchPage = false
	legacy.Limit = 100
	want := read(legacy)
	if first.Total != 8 || want.Total != first.Total || first.Options != nil || want.Options == nil {
		t.Fatalf("wrong read contract: totals %d/%d, first options %t, legacy options %t",
			first.Total, want.Total, first.Options != nil, want.Options != nil)
	}
	var position int
	for page := first; ; page = read(q) {
		if page.Total != want.Total {
			t.Fatalf("total changed between pages: %d", page.Total)
		}
		for _, row := range page.Records {
			if position >= len(want.Records) || !proto.Equal(row, want.Records[position]) {
				t.Fatalf("distinct-time/tied-time order mismatch at row %d", position)
			}
			position++
		}
		if page.NextCursor == "" {
			break
		}
		q.Cursor = page.NextCursor
	}
	if position != 8 {
		t.Fatalf("lost rows across pages: %d", position)
	}
	detailFilter := proto.Clone(q).(*pb.LogQuery)
	detailFilter.Cursor = ""
	if _, err := c.GetLogDetail(ctx, &pb.LogDetailRequest{
		Query: detailFilter, RecordId: first.Records[0].RecordId,
	}); err != nil {
		t.Fatal("workbench row could not open its frozen detail", err)
	}
	replacement := proto.Clone(q).(*pb.LogQuery)
	replacement.ReadToken, replacement.Cursor, replacement.RecoveryOffset = "", "", 99
	last := read(replacement)
	if last.Total != 9 || len(last.Records) != 3 || !proto.Equal(last.Records[0], want.Records[5]) {
		t.Fatalf("replacement did not clamp to the last page")
	}
	replacement.ReadToken, replacement.Cursor = last.ReadToken, first.NextCursor
	if _, err := c.QueryLogs(ctx, replacement); err == nil {
		t.Fatal("accepted a cursor from another frozen read")
	}
}
