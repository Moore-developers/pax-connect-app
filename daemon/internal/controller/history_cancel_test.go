package controller

import (
	"context"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"testing"
	"time"
)

func TestCancelledHistoryQueriesDoNotShareResults(t *testing.T) {
	ctx := context.Background()
	c, _ := newLogsTestController(t, newLogsTestDir(t))
	for i := 0; i < 100; i++ {
		if e := c.RecordEvent(ctx, config.HistoryEvent{ID: fmt.Sprint(i), At: time.Now().Add(-time.Second), Kind: "operation", Source: "Pax", Title: "事件", Level: "info"}); e != nil {
			t.Fatal(e)
		}
	}
	page, e := c.QueryLogs(ctx, &pb.LogQuery{View: "activity"})
	if e != nil {
		t.Fatal(e)
	}
	if len(page.Records) == 0 {
		t.Fatal("missing seeded record")
	}
	var cancelled [4]int
	for i := 0; i < 3000; i++ {
		qctx, cancel := context.WithCancel(ctx)
		timer := time.AfterFunc(time.Duration(i%50+1)*time.Microsecond, cancel)
		switch i % 4 {
		case 0:
			v, err := c.QueryLogs(qctx, &pb.LogQuery{View: "activity"})
			if cancelledReply(t, v, err) {
				cancelled[0]++
			}
		case 1:
			v, err := c.QueryLogObjects(qctx, &pb.LogQuery{View: "activity", Dimension: "system"})
			if cancelledReply(t, v, err) {
				cancelled[1]++
			}
		case 2:
			v, err := c.GetLogDetail(qctx, &pb.LogDetailRequest{RecordId: page.Records[0].RecordId, Query: &pb.LogQuery{View: "activity", ReadToken: page.ReadToken}})
			if cancelledReply(t, v, err) {
				cancelled[2]++
			}
		case 3:
			v, err := c.QueryTraffic(qctx, &pb.TrafficQuery{Window: "day", Dimension: "domain"})
			if cancelledReply(t, v, err) {
				cancelled[3]++
			}
		}
		timer.Stop()
		cancel()
	}

	for i, count := range cancelled {
		if count == 0 {
			t.Errorf("query kind %d did not exercise cancellation", i)
		}
	}
}

func cancelledReply[T any](t *testing.T, value *T, err error) bool {
	t.Helper()
	if status.Code(err) != codes.Canceled && status.Code(err) != codes.DeadlineExceeded {
		return false
	}
	if value != nil {
		t.Fatal("cancelled RPC exposed owner-owned result")
	}
	return true
}
