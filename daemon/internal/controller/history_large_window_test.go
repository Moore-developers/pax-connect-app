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

func TestDayLogQueryDoesNotBudgetRawDiagnosticsAsListSnapshot(t *testing.T) {
	ctx := context.Background()
	c, p := newLogsTestController(t, newLogsTestDir(t))
	now := time.Now()
	events := make([]config.HistoryEvent, 0, 800)
	for i := 0; i < 800; i++ {
		raw := make([]string, 8)
		for j := range raw {
			raw[j] = strings.Repeat("diagnostic ", 400)
		}
		events = append(events, config.HistoryEvent{ID: fmt.Sprintf("large-%04d", i), At: now.Add(-time.Duration(i+1) * time.Minute), Kind: "diagnostic", Title: "retained event", Level: "info", Source: "fixture", Raw: raw})
	}
	p.emit(events...)
	query := &pb.LogQuery{View: "activity", From: timestamppb.New(now.Add(-24 * time.Hour)), Until: timestamppb.New(now), Limit: 100}
	page, err := c.QueryLogs(ctx, query)
	if err != nil {
		t.Fatalf("24-hour retained logs must remain pageable: %v", err)
	}
	if page.Total != 800 || len(page.Records) != 100 || page.NextCursor == "" {
		t.Fatalf("incomplete first page total=%d records=%d", page.Total, len(page.Records))
	}
	detail, err := c.GetLogDetail(ctx, &pb.LogDetailRequest{RecordId: page.Records[0].RecordId, Query: &pb.LogQuery{View: "activity", ReadToken: page.ReadToken, From: query.From, Until: query.Until}})
	if err != nil || len(detail.GetRaw()) != 8 || detail.GetTotalEvents() != 1 {
		t.Fatalf("lazy detail lost evidence: %v", err)
	}
	query.ReadToken, query.Cursor = page.ReadToken, page.NextCursor
	next, err := c.QueryLogs(ctx, query)
	if err != nil || len(next.GetRecords()) != 100 || next.Records[0].RecordId == page.Records[0].RecordId {
		t.Fatalf("frozen second page failed: %v", err)
	}
}
