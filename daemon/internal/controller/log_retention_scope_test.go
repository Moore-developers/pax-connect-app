package controller

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

func TestLogRetentionKeepsRecentTokenAndRevokesAffectedQuery(t *testing.T) {
	ctx := context.Background()
	c, p := newLogsTestController(t, newLogsTestDir(t))
	now := time.Now()
	p.emit(config.HistoryEvent{ID: "old", At: now.Add(-2 * time.Hour), Source: "Pax", Kind: "operation", Title: "old", Level: "info"})
	p.emit(config.HistoryEvent{ID: "recent-2", At: now.Add(-2 * time.Minute), Source: "Pax", Kind: "operation", Title: "recent-2", Level: "info"})
	p.emit(config.HistoryEvent{ID: "recent", At: now.Add(-time.Minute), Source: "Pax", Kind: "operation", Title: "recent", Level: "info"})
	recent := &pb.LogQuery{View: "activity", From: timestamppb.New(now.Add(-30 * time.Minute)), Until: timestamppb.New(now), Limit: 1}
	page, err := c.QueryLogs(ctx, recent)
	if err != nil || page.Total != 2 {
		t.Fatal(page, err)
	}
	broad := &pb.LogQuery{View: "activity", From: timestamppb.New(now.Add(-3 * time.Hour)), Until: timestamppb.New(now)}
	affected, err := c.QueryLogs(ctx, broad)
	if err != nil || affected.Total != 3 {
		t.Fatal(affected, err)
	}
	late, _, err := c.logRead(ctx, recent)
	if err != nil {
		t.Fatal(err)
	}
	l := stopWriterGoroutine(t, c)
	pruned, err := l.logs.Prune(ctx, now.Add(-time.Hour), historystore.Limits{Rows: 2000, Duration: time.Second})
	if err != nil || pruned.Deleted == 0 {
		t.Fatal(pruned, err)
	}
	if err = l.bumpDeletion(ctx); err != nil {
		t.Fatal(err)
	}
	recent.ReadToken = page.ReadToken
	kept, err := c.QueryLogs(ctx, recent)
	if err != nil || kept.Total != 2 {
		t.Fatal("unrelated expiry revoked recent page", kept, err)
	}
	recent.Cursor = page.NextCursor
	next, err := c.QueryLogs(ctx, recent)
	if err != nil || len(next.Records) != 1 || next.Records[0].RecordId == page.Records[0].RecordId {
		t.Fatal("pagination did not retain its snapshot", next, err)
	}
	recent.Cursor = ""
	if _, err = c.readTokens.save(late); err != nil {
		t.Fatal("safe in-flight read rejected", err)
	}
	broad.ReadToken = affected.ReadToken
	if _, err = c.QueryLogs(ctx, broad); err == nil {
		t.Fatal("deleted facts survived")
	}
	// Advancing the prefix into this query revokes it too.
	if _, err = l.logs.Prune(ctx, now.Add(-90*time.Second), historystore.Limits{Rows: 2000, Duration: time.Second}); err != nil {
		t.Fatal(err)
	}
	if err = l.bumpDeletion(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = c.QueryLogs(ctx, recent); err == nil {
		t.Fatal("intersecting prefix retained token")
	}
	recent.ReadToken = ""
	current, err := c.QueryLogs(ctx, recent)
	if err != nil || current.Total != 1 {
		t.Fatal(current, err)
	}
	recent.ReadToken = current.ReadToken
	// A manual clear must still revoke the surviving recent token.
	if err = l.logs.Update(ctx, func(tx *historystore.LogsTx) error { return tx.ClearLogTables(ctx) }); err != nil {
		t.Fatal(err)
	}
	if _, err = c.QueryLogs(ctx, recent); err == nil {
		t.Fatal("clear retained private facts")
	}
}
