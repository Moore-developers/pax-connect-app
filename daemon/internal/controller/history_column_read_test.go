package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

func TestColumnReadBytesCommitFreezesOldTokenAndSharesUnchangedFacts(t *testing.T) {
	ctx := context.Background()
	c, p := newLogsTestController(t, newLogsTestDir(t))
	at := time.Now().Add(-time.Minute)
	f := config.FlowFact{Key: "s/1/model", StartedAt: at, Domain: "example.test", Direction: "direct"}
	p.observe(config.ObservationBatch{Session: "s", HighWater: 1, Collecting: true, Records: []config.Observation{{Session: "s", Sequence: 1, Kind: "started", At: at, Flow: &f}}})
	before, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.readTokens.take(before.ReadToken, "logs")
	if err != nil || r.columnSnapshot == nil {
		t.Fatal("production column snapshot was not attached", err)
	}
	prior := r.columnSnapshot.Meta()
	p.observe(config.ObservationBatch{Session: "s", HighWater: 2, Collecting: true, Records: []config.Observation{{Session: "s", Sequence: 2, Kind: "sample", At: at.Add(time.Second), Flow: &f, Upload: 321, Download: 654}}})
	latest, err := c.logs.logColumnSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = latest.Close() }()
	if latest.Meta().LogsRevision != prior.LogsRevision {
		t.Fatal("fixture changed public log revision")
	}
	oldFact, ok, err := r.columnSnapshot.Connection(ctx, f.Key)
	if err != nil || !ok || oldFact.Metrics.Upload != 0 {
		t.Fatal("old summary changed", err)
	}
	newFact, ok, err := latest.Connection(ctx, f.Key)
	if err != nil || !ok || newFact.Metrics.Upload != 321 {
		t.Fatal("new summary missing byte update", err)
	}
	after, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity", ReadToken: before.ReadToken})
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(before, after) {
		t.Fatal("old token changed after byte-only update")
	}
	fresh, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity"})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Records[0].Metrics.Upload != 321 || before.Records[0].Metrics.Upload != 0 {
		t.Fatal("new/old bytes mixed")
	}
	// A returned row may be modified by its caller; it must not modify the shared base.
	fresh.Records[0].Title = "caller mutation"
	repeat, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity", ReadToken: fresh.ReadToken})
	if err != nil || repeat.Records[0].Title == "caller mutation" {
		t.Fatal("shared projection leaked mutable response", err)
	}
}

func TestColumnReadDeletionRevokesAllVersionsAndLateSave(t *testing.T) {
	ctx := context.Background()
	c, p := newLogsTestController(t, newLogsTestDir(t))
	p.emit(config.HistoryEvent{ID: "one", At: time.Now(), Source: "Pax", Kind: "operation", Title: "one", Level: "info"})
	first, _, err := c.columnLogRead(ctx, &pb.LogQuery{View: "activity"})
	if err != nil {
		t.Fatal(err)
	}
	p.emit(config.HistoryEvent{ID: "two", At: time.Now(), Source: "Pax", Kind: "operation", Title: "two", Level: "info"})
	second, _, err := c.columnLogRead(ctx, &pb.LogQuery{View: "activity"})
	if err != nil {
		t.Fatal(err)
	}
	l := stopWriterGoroutine(t, c)
	if err = l.logs.Update(ctx, func(tx *historystore.LogsTx) error { return tx.ClearLogTables(ctx) }); err != nil {
		t.Fatal(err)
	}
	for _, r := range []*historyRead{first, second} {
		if !r.snapshotRevoked() {
			t.Fatal("old version remained valid after committed clear")
		}
		if _, err := c.readTokens.save(r); err == nil {
			t.Fatal("late old read resurrected after clear")
		}
	}
	page, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity"})
	if err != nil || page.Total != 0 {
		t.Fatal("clear did not publish the empty retained snapshot", page, err)
	}
}

func TestColumnReadLargerThanLegacySnapshotIsReadable(t *testing.T) {
	ctx := context.Background()
	c, _ := newLogsTestController(t, newLogsTestDir(t))
	l := stopWriterGoroutine(t, c)
	title := strings.Repeat("history", 2400)
	const count = 1600
	err := l.logs.Update(ctx, func(tx *historystore.LogsTx) error {
		for i := 0; i < count; i++ {
			e := config.HistoryEvent{ID: fmt.Sprintf("large-%04d", i), At: time.Now().Add(-time.Minute), Source: "Core", Kind: "diagnostic", Title: title, Level: "info"}
			if _, _, err := tx.AppendEvent(ctx, e); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != count || len(page.Records) != 20 || page.NextCursor == "" {
		t.Fatal("large history was truncated or reported empty", page.Total, len(page.Records))
	}
	next, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity", Limit: 20, ReadToken: page.ReadToken, Cursor: page.NextCursor})
	if err != nil || len(next.Records) != 20 || next.Records[0].RecordId == page.Records[0].RecordId {
		t.Fatal("large history did not page", err)
	}
	detail, err := c.GetLogDetail(ctx, &pb.LogDetailRequest{RecordId: page.Records[0].RecordId, Query: &pb.LogQuery{View: "activity", ReadToken: page.ReadToken}})
	if err != nil || detail.TotalEvents != 1 || len(detail.Timeline) != 1 {
		t.Fatal("large history did not preserve original event detail", err)
	}
	r, err := c.readTokens.take(page.ReadToken, "logs")
	if err != nil || r.bytes > 1<<20 {
		t.Fatal("token charged a complete private payload", r.bytes, err)
	}
}

func TestColumnReadSnapshotHonorsDeadline(t *testing.T) {
	ctx := context.Background()
	c, _ := newLogsTestController(t, newLogsTestDir(t))
	l := stopWriterGoroutine(t, c)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- l.logs.Update(ctx, func(tx *historystore.LogsTx) error { close(entered); <-release; return nil })
	}()
	<-entered
	limited, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	_, err := l.logColumnSnapshot(limited)
	rawCtx, rawCancel := context.WithTimeout(ctx, 20*time.Millisecond)
	_, rawErr := c.GetLogDetail(rawCtx, &pb.LogDetailRequest{EventId: "not-an-admitted-event"})
	rawCancel()
	if status.Code(rawErr) != codes.DeadlineExceeded {
		t.Error("raw event detail bypassed the pending commit barrier", rawErr)
	}
	close(release)
	if updateErr := <-done; updateErr != nil {
		t.Fatal(updateErr)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("reader waited beyond its request deadline", err)
	}
}

func TestColumnReadDNSGroupAndMemberCursorsAcrossWindows(t *testing.T) {
	ctx := context.Background()
	c, p := newLogsTestController(t, newLogsTestDir(t))
	at := time.Now().Add(-time.Minute)
	var records []config.Observation
	for i := 0; i < 9; i++ {
		f := config.FlowFact{Key: fmt.Sprintf("dns/model/%d", i), Purpose: "dns", Domain: fmt.Sprintf("group-%d.example.test", i/3), StartedAt: at.Add(time.Duration(i) * time.Millisecond), ResponseKnown: true, ResponseMS: 5, DNS: &config.DNSFact{QueryType: "A", Result: "NOERROR", Answers: []string{"192.0.2.1"}}}
		records = append(records, config.Observation{Session: "dns", Sequence: uint64(i + 1), Kind: "started", At: f.StartedAt, Flow: &f})
	}
	p.observe(config.ObservationBatch{Session: "dns", HighWater: 9, Collecting: true, Records: records})
	for _, window := range []string{"day", "today", "week", "month"} {
		t.Run(window, func(t *testing.T) {
			page, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "system", Window: window})
			if err != nil || page.DnsSummary == nil {
				t.Fatal("missing DNS summary", err)
			}
			request := &pb.TrafficDetailRequest{RowId: page.DnsSummary.Id, ReadToken: page.ReadToken, Limit: 1}
			first, err := c.QueryTrafficDetail(ctx, request)
			if err != nil || len(first.DnsGroups) != 1 || first.NextCursor == "" {
				t.Fatal("missing first group page", err)
			}
			request.Cursor = first.NextCursor
			next, err := c.QueryTrafficDetail(ctx, request)
			if err != nil || len(next.DnsGroups) != 1 || next.DnsGroups[0].Id == first.DnsGroups[0].Id {
				t.Fatal("DNS group cursor rejected or repeated", err)
			}
			request.Cursor = ""
			request.DnsGroupId = first.DnsGroups[0].Id
			members, err := c.QueryTrafficDetail(ctx, request)
			if err != nil || len(members.Items) != 1 || members.NextCursor == "" {
				t.Fatal("missing member page", err)
			}
			request.Cursor = members.NextCursor
			more, err := c.QueryTrafficDetail(ctx, request)
			if err != nil || len(more.Items) != 1 || more.Items[0].Key == members.Items[0].Key {
				t.Fatal("DNS member cursor rejected or repeated", err)
			}
		})
	}
}
