package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

func TestColumnLogObjectPaginationKeepsSelection(t *testing.T) {
	ctx := context.Background()
	c, pipeline := newLogsTestController(t, newLogsTestDir(t))
	c.history.applicationInventory = &historyApplicationInventory{At: time.Now(), Apps: []config.RoutingApplication{{ID: "example.browser", Name: "Browser", Path: "/Applications/Browser.app", Bundle: true}}}
	at := time.Now().Add(-time.Minute)
	domains := []string{"alpha.test", "beta.test", "beta.test", "gamma.test"}
	batch := config.ObservationBatch{Session: "object-pages", HighWater: uint64(len(domains))}
	for i, domain := range domains {
		flow := config.FlowFact{Key: fmt.Sprintf("object-pages/%d", i), Domain: domain, IP: "192.0.2.1", Transport: "tcp", Direction: "direct", StartedAt: at.Add(time.Duration(i) * time.Second)}
		if i == 2 {
			flow.ApplicationID = "example.browser"
			flow.Process = "/Applications/Browser.app/Contents/MacOS/Browser"
		}
		batch.Records = append(batch.Records, config.Observation{Session: batch.Session, Sequence: uint64(i + 1), At: flow.StartedAt, Kind: "started", Flow: &flow})
	}
	pipeline.observe(batch)
	q := &pb.LogQuery{View: "activity", Dimension: "domain", Scope: "access", From: timestamppb.New(at.Add(-time.Second)), Until: timestamppb.New(at.Add(time.Minute)), Limit: 1}
	first, err := c.QueryLogObjects(ctx, q)
	if err != nil || len(first.Items) != 1 || first.Items[0].Id != "beta.test" || first.Items[0].Count != 2 || first.Items[0].Symbol != "app" || first.NextCursor == "" {
		t.Fatalf("first directory page: %v %v", first, err)
	}
	q.ReadToken, q.Cursor = first.ReadToken, first.NextCursor
	second, err := c.QueryLogObjects(ctx, q)
	if err != nil || len(second.Items) != 1 || second.Items[0].Id != "alpha.test" || second.Items[0].Symbol != "gearshape" || second.NextCursor == "" {
		t.Fatalf("second directory page: %v %v", second, err)
	}
	q.Cursor = second.NextCursor
	third, err := c.QueryLogObjects(ctx, q)
	if err != nil || len(third.Items) != 1 || third.Items[0].Id != "gamma.test" || third.NextCursor != "" {
		t.Fatalf("third directory page: %v %v", third, err)
	}
	_, signature, _ := strings.Cut(first.NextCursor, ".")
	q.Cursor = "999." + signature
	if _, err = c.QueryLogObjects(ctx, q); err == nil || !strings.Contains(err.Error(), "history_cursor_expired") {
		t.Fatalf("out-of-range directory cursor: %v", err)
	}
	q.Cursor, q.Search, q.ObjectId = "", "alpha", "gamma.test"
	filtered, err := c.QueryLogObjects(ctx, q)
	if err != nil || len(filtered.Items) != 1 || filtered.Items[0].Id != "alpha.test" || filtered.SelectedObject == nil || filtered.SelectedObject.Id != "gamma.test" {
		t.Fatalf("selected object outside search: %v %v", filtered, err)
	}
	q.Search = "does-not-match"
	empty, err := c.QueryLogObjects(ctx, q)
	if err != nil || len(empty.Items) != 0 || empty.SelectedObject == nil || empty.SelectedObject.Id != "gamma.test" || empty.TotalEvents != first.TotalEvents {
		t.Fatalf("empty search preserves selection and total: %v %v", empty, err)
	}
}

func TestColumnLogObjectPaginationFreezesDirectory(t *testing.T) {
	ctx := context.Background()
	c, pipeline := newLogsTestController(t, newLogsTestDir(t))
	at := time.Now().Add(-time.Minute)
	batch := config.ObservationBatch{Session: "frozen-directory", HighWater: 2}
	for i, domain := range []string{"first.test", "second.test"} {
		flow := config.FlowFact{Key: fmt.Sprintf("frozen-directory/%d", i), Domain: domain, IP: "192.0.2.1", Transport: "tcp", Direction: "direct", StartedAt: at.Add(time.Duration(i) * time.Second)}
		batch.Records = append(batch.Records, config.Observation{Session: batch.Session, Sequence: uint64(i + 1), At: flow.StartedAt, Kind: "started", Flow: &flow})
	}
	pipeline.observe(batch)
	q := &pb.LogQuery{View: "activity", Dimension: "domain", Scope: "access", From: timestamppb.New(at.Add(-time.Second)), Until: timestamppb.New(at.Add(time.Minute)), Limit: 1}
	first, err := c.QueryLogObjects(ctx, q)
	if err != nil || len(first.Items) != 1 || first.Items[0].Id != "first.test" || first.NextCursor == "" {
		t.Fatalf("first page: %v %v", first, err)
	}
	added := config.FlowFact{Key: "frozen-directory/new", Domain: "added.test", IP: "192.0.2.1", Transport: "tcp", Direction: "direct", StartedAt: at.Add(3 * time.Second)}
	pipeline.observe(config.ObservationBatch{Session: batch.Session, HighWater: 3, Records: []config.Observation{{Session: batch.Session, Sequence: 3, At: added.StartedAt, Kind: "started", Flow: &added}}})
	q.ReadToken, q.Cursor = first.ReadToken, first.NextCursor
	second, err := c.QueryLogObjects(ctx, q)
	if err != nil || len(second.Items) != 1 || second.Items[0].Id != "second.test" || second.NextCursor != "" || second.TotalEvents != first.TotalEvents {
		t.Fatalf("frozen second page: %v %v", second, err)
	}
	q.ReadToken, q.Cursor = "", ""
	fresh, err := c.QueryLogObjects(ctx, q)
	if err != nil || len(fresh.Items) != 1 || fresh.Items[0].Id != "added.test" || fresh.TotalEvents != first.TotalEvents+1 || fresh.NextCursor == "" {
		t.Fatalf("fresh directory: %v %v", fresh, err)
	}
}
