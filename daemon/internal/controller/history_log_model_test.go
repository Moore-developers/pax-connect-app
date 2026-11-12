package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

func TestLogReadModelBytesCommitFreezesOldTokenAndSharesUnchangedFacts(t *testing.T) {
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
	if err != nil || r.logVersion == nil {
		t.Fatal("production model was not attached", err)
	}
	prior := r.logVersion
	p.observe(config.ObservationBatch{Session: "s", HighWater: 2, Collecting: true, Records: []config.Observation{{Session: "s", Sequence: 2, Kind: "sample", At: at.Add(time.Second), Flow: &f, Upload: 321, Download: 654}}})
	latest, err := c.logs.readModel(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if latest.serial <= prior.serial {
		t.Fatal("byte-only commit failed to publish an internal version")
	}
	if latest.meta.LogsRevision != prior.meta.LogsRevision {
		t.Fatal("fixture unexpectedly changed public log revision")
	}
	for id, e := range prior.events {
		if latest.events[id] != e {
			t.Fatal("unchanged immutable event was copied")
		}
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

func TestLogReadModelDeletionRevokesAllVersionsAndLateSave(t *testing.T) {
	ctx := context.Background()
	c, p := newLogsTestController(t, newLogsTestDir(t))
	p.emit(config.HistoryEvent{ID: "one", At: time.Now(), Source: "Pax", Kind: "operation", Title: "one", Level: "info"})
	first, _, err := c.logRead(ctx, &pb.LogQuery{View: "activity"})
	if err != nil {
		t.Fatal(err)
	}
	p.emit(config.HistoryEvent{ID: "two", At: time.Now(), Source: "Pax", Kind: "operation", Title: "two", Level: "info"})
	second, _, err := c.logRead(ctx, &pb.LogQuery{View: "activity"})
	if err != nil {
		t.Fatal(err)
	}
	l := stopWriterGoroutine(t, c)
	if err = l.logs.Update(ctx, func(tx *historystore.LogsTx) error { return tx.ClearLogTables(ctx) }); err != nil {
		t.Fatal(err)
	}
	for _, r := range []*historyRead{first, second} {
		if r.logVersion.valid.Load() {
			t.Fatal("old version remained valid after committed clear")
		}
		if _, err := c.readTokens.save(r); err == nil {
			t.Fatal("late old read resurrected after clear")
		}
	}
	page, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity"})
	if err != nil || page.Total != 0 {
		t.Fatal("clear did not publish the empty retained model", page, err)
	}
}

func TestLogReadModelLargerThanLegacySnapshotIsReadable(t *testing.T) {
	ctx := context.Background()
	c, _ := newLogsTestController(t, newLogsTestDir(t))
	l := stopWriterGoroutine(t, c)
	title := strings.Repeat("history", 2400)
	const count = 1600
	err := l.logs.Update(ctx, func(tx *historystore.LogsTx) error {
		for i := 0; i < count; i++ {
			e := config.HistoryEvent{ID: fmt.Sprintf("large-%04d", i), At: time.Now().Add(-time.Minute), Source: "Core", Kind: "diagnostic", Title: title, Level: "info"}
			body, err := json.Marshal(e)
			if err != nil {
				return err
			}
			if _, _, err = tx.PutEvent(ctx, historystore.LogEvent{EventID: e.ID, AtMs: e.At.UnixMilli(), Body: body}); err != nil {
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

func TestLogReadModelFailedPublicationCannotReturnEmptyOrStale(t *testing.T) {
	ctx := context.Background()
	c, p := newLogsTestController(t, newLogsTestDir(t))
	at := time.Now().Add(-time.Minute)
	f := config.FlowFact{Key: "s/failure", StartedAt: at, Domain: "example.test", Direction: "direct"}
	p.observe(config.ObservationBatch{Session: "s", HighWater: 1, Records: []config.Observation{{Session: "s", Sequence: 1, Kind: "started", At: at, Flow: &f}}})
	l := stopWriterGoroutine(t, c)
	row := historystore.LogConnection{FlowKey: f.Key, StartedMs: at.UnixMilli(), LastActivityMs: at.UnixMilli(), Body: []byte("broken JSON")}
	if err := l.logs.Update(ctx, func(tx *historystore.LogsTx) error { return tx.PutConnection(ctx, row) }); err != nil {
		t.Fatal(err)
	}
	if page, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity"}); err == nil {
		t.Fatal("failed index publication returned a successful page", page)
	}
	body, err := json.Marshal(config.LogConnectionSummary{Fact: f})
	if err != nil {
		t.Fatal(err)
	}
	row.Body = body
	if err = l.logs.Update(ctx, func(tx *historystore.LogsTx) error { return tx.PutConnection(ctx, row) }); err != nil {
		t.Fatal(err)
	}
	if page, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity"}); err != nil || page.Total != 1 {
		t.Fatal("owner did not rebuild complete retained data after recovery", err)
	}
}

func TestLogReadModelReadBarrierHonorsDeadline(t *testing.T) {
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
	_, err := l.readModel(limited)
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

func TestLogReadModelDNSGroupAndMemberCursorsAcrossWindows(t *testing.T) {
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

func TestLogReadModelBudgetChargesCurrentAndEvictsOldToken(t *testing.T) {
	valid := &atomic.Bool{}
	valid.Store(true)
	old := &logModelVersion{serial: 1, valid: valid, bytes: logModelBudget - 1024, connections: map[string]*logModelConnection{"old": {bytes: 1024}}}
	current := &logModelVersion{serial: 2, valid: valid, bytes: logModelBudget - 512}
	cache := newReadTokenCache()
	cache.modelCurrent = current
	r := &historyRead{kind: "logs", logVersion: old, bytes: 64}
	if _, err := cache.save(r); err == nil {
		t.Fatal("a new token retained old payload beyond the shared model budget")
	}
	cache.reads["older"] = &historyRead{kind: "logs", logVersion: old, timeUsed: time.Now()}
	cache.makeLogModelRoom(current)
	if len(cache.reads) != 0 {
		t.Fatal("old token prevented current model from fitting its resource budget")
	}
	if charged := modelRetainedBytes(current, cache); charged != current.bytes {
		t.Fatal("candidate version was not the budget base", charged)
	}
}

func TestLogReadModelEvictionCommitCannotReturnOldTokenBeforeRevocation(t *testing.T) {
	ctx := context.Background()
	c, p := newLogsTestController(t, newLogsTestDir(t))
	for i := 0; i < 3; i++ {
		p.emit(config.HistoryEvent{ID: fmt.Sprintf("evict-%d", i), At: time.Now().Add(time.Duration(i-10) * time.Second), Source: "Pax", Kind: "operation", Title: "retained", Level: "info"})
	}
	page, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.readTokens.take(page.ReadToken, "logs")
	if err != nil {
		t.Fatal(err)
	}
	l := stopWriterGoroutine(t, c)
	source := l.logs.(*historystore.Logs)
	committed, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var rebuildErr error
	// Pause at the real SQL commit/publication boundary. Rows=1 forces this eviction
	// through several committed prune transactions before the delayed notification.
	source.ObserveReads(func(ctx context.Context, d historystore.LogsChanges) {
		if !d.Reset {
			l.model.publish(ctx, d)
			return
		}
		close(committed)
		<-release
		l.model.revoke()
		snapshot, err := source.ReadSnapshot(ctx)
		if err != nil {
			rebuildErr = err
			return
		}
		l.model.publish(ctx, snapshot)
	})
	done := make(chan error, 1)
	go func() {
		_, err := source.EvictOldest(ctx, 0, historystore.Limits{Rows: 1, Duration: time.Second})
		done <- err
	}()
	<-committed
	if _, ok, err := source.ReadLogEvent(ctx, "evict-0"); err != nil || ok {
		t.Fatal("fixture did not cross an actual committed deletion", err)
	}
	if !r.logVersion.valid.Load() {
		t.Fatal("fixture did not pause before model revocation")
	}
	limited, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	_, err = c.QueryLogs(limited, &pb.LogQuery{View: "activity", ReadToken: page.ReadToken})
	cancel()
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatal("old token escaped while eviction publication was pending", err)
	}
	once.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if rebuildErr != nil {
		t.Fatal(rebuildErr)
	}
	if _, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity", ReadToken: page.ReadToken}); err == nil {
		t.Fatal("evicted token survived final revocation")
	}
	fresh, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity"})
	if err != nil || fresh.Total != 0 {
		t.Fatal("post-eviction version did not match retained SQLite facts", err)
	}
}
