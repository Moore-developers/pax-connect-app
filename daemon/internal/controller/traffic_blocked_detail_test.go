package controller

import (
	"context"
	"encoding/json"
	"fmt"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
	"testing"
	"time"
)

func TestBlockedTrafficRetainedDetailsArePagedFromRealSummaries(t *testing.T) {
	c, _ := newTrafficRPCFixture(t)
	ctx := context.Background()
	now := time.Now().Add(-time.Minute)
	var records []config.Observation
	for i := 0; i < 25; i++ {
		at := now.Add(time.Duration(i) * time.Second)
		closed := at.Add(time.Millisecond)
		f := config.FlowFact{Key: fmt.Sprintf("blocked/%02d", i), ApplicationID: "fixture.blocked", Application: "Blocked App", StartedAt: at, ClosedAt: &closed, Direction: "blocked", EstablishmentState: "failed", Failure: "rule_rejected", Rule: "builtin=IPv6 流量", RuleSource: "Pax 包：Geo 基础包"}
		identified := f
		identified.Direction, identified.EstablishmentState, identified.Failure, identified.Rule, identified.RuleSource = "unknown", "not_measurable", "", "", ""
		identified.ClosedAt = nil
		records = append(records,
			config.Observation{Session: "blocked", Sequence: uint64(i*3 + 1), At: at.Add(-time.Millisecond), Kind: "identity", Flow: &identified},
			config.Observation{Session: "blocked", Sequence: uint64(i*3 + 2), At: at, Kind: "rejected", Flow: &f},
			config.Observation{Session: "blocked", Sequence: uint64(i*3 + 3), At: closed, Kind: "closed", Flow: &f})
	}
	if err := FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "blocked", HighWater: 75, Records: records}); err != nil {
		t.Fatal(err)
	}
	frozen := &historyRead{at: time.Now()}
	if err := c.freezeTrafficConnections(ctx, frozen, ""); err != nil {
		t.Fatal(err)
	}
	t.Logf("retained connection summaries=%d", len(frozen.aggregateFlows))
	if len(frozen.aggregateFlows) != 25 {
		t.Fatal("rejected observations did not persist summaries")
	}
	for _, window := range []string{"day", "today", "week"} {
		page, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: window})
		if err != nil || len(page.GetItems()) != 1 {
			t.Fatal(page, err)
		}
		row := page.Items[0]
		t.Logf("%s row direction=%s route=%q failed=%d count=%d", window, row.Direction, row.RouteId, row.FailedCount, row.Connections)
		if row.FailedCount != 25 || row.Connections != 0 || row.Upload != 0 || row.Download != 0 {
			t.Fatal("rejected counters changed", row)
		}
		detail, err := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: page.ReadToken, RowId: row.Id})
		if err != nil || detail.GetRetainedConnectionCount() != 25 || len(detail.GetItems()) != 20 || detail.GetNextCursor() == "" {
			t.Fatalf("%s retained=%d items=%d cursor=%t: %v", window, detail.GetRetainedConnectionCount(), len(detail.GetItems()), detail.GetNextCursor() != "", err)
		}
		if len(detail.RecentResponses) != 0 {
			t.Fatal("rejections invented responses")
		}
		next, err := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: page.ReadToken, RowId: row.Id, Cursor: detail.NextCursor})
		if err != nil || len(next.GetItems()) != 5 {
			t.Fatal("remaining rejected page missing", err)
		}
		seen := map[string]bool{}
		for _, item := range append(detail.Items, next.Items...) {
			if seen[item.Key] || item.Direction != "blocked" {
				t.Fatal("duplicate or wrong-direction member")
			}
			seen[item.Key] = true
		}
	}
	oldPage, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: "today"})
	if err != nil {
		t.Fatal(err)
	}
	late := config.FlowFact{Key: "blocked/new", ApplicationID: "fixture.blocked", Application: "Blocked App", StartedAt: time.Now().Add(-time.Second), Direction: "blocked", Failure: "rule_rejected", EstablishmentState: "failed"}
	if err = FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "blocked", HighWater: 76, Records: []config.Observation{{Session: "blocked", Sequence: 76, At: late.StartedAt, Kind: "rejected", Flow: &late}}}); err != nil {
		t.Fatal(err)
	}
	frozenPage, err := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: oldPage.ReadToken, RowId: oldPage.Items[0].Id})
	if err != nil || frozenPage.GetRetainedConnectionCount() != 25 || frozenPage.Summary.FailedCount != 25 {
		t.Fatal("old token changed", frozenPage, err)
	}
	fresh, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: "today"})
	if err != nil {
		t.Fatal(err)
	}
	freshDetail, err := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: fresh.ReadToken, RowId: fresh.Items[0].Id})
	if err != nil || freshDetail.GetRetainedConnectionCount() != 26 || freshDetail.Summary.FailedCount != 26 {
		t.Fatal("new token missed retained rejection", freshDetail, err)
	}
	direct, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: "today", Direction: "direct"})
	if err != nil || len(direct.GetItems()) != 0 {
		t.Fatal("blocked facts crossed direction filter", direct, err)
	}

}

func TestLegacyBlockedDetailProjectionNeedsItsOwnRetainedEvent(t *testing.T) {
	c, _ := newTrafficRPCFixture(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Minute)
	var observations []config.Observation
	for i := 0; i < 5; i++ {
		f := config.FlowFact{Key: fmt.Sprintf("legacy-reject/%d", i), ApplicationID: "fixture.legacy", Application: "Legacy App", StartedAt: at, Direction: "blocked", Failure: "rule_rejected", EstablishmentState: "failed", Rule: "builtin=IPv6 流量", RuleSource: "Pax 包：Geo 基础包"}
		observations = append(observations, config.Observation{Session: "legacy-reject", Sequence: uint64(i + 1), At: at, Kind: "rejected", Flow: &f})
	}
	if err := FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "legacy-reject", HighWater: 5, Records: observations}); err != nil {
		t.Fatal(err)
	}
	job, err := c.logJob()
	if err != nil {
		t.Fatal(err)
	}
	// Persist exactly the older broken fold. The original event remains independently stored.
	if err = job.logs.Update(ctx, func(tx *historystore.LogsTx) error {
		rows, e := tx.Connections(ctx)
		if e != nil {
			return e
		}
		for i, row := range rows {
			var summary config.LogConnectionSummary
			if e = json.Unmarshal(row.Body, &summary); e != nil {
				return e
			}
			summary.Fact.Direction, summary.Fact.Rule, summary.Fact.RuleSource = "unknown", "", ""
			if i == 1 {
				summary.LastEventID = "missing-event"
			}
			if i == 2 {
				summary.LastEventID = "legacy-reject:1"
			} // another connection cannot authorize this one
			if i == 3 {
				summary.Fact.Failure = ""
			}
			if i == 4 {
				future := config.HistoryEvent{ID: "future-rejection", At: time.Now().Add(time.Hour), Kind: "rejected", Flow: &config.FlowFact{Key: summary.Fact.Key, Direction: "blocked", Failure: "rule_rejected"}}
				body, marshalErr := json.Marshal(future)
				if marshalErr != nil {
					return marshalErr
				}
				if _, _, putErr := tx.PutEvent(ctx, historystore.LogEvent{EventID: future.ID, AtMs: future.At.UnixMilli(), FlowKey: summary.Fact.Key, Kind: future.Kind, Body: body}); putErr != nil {
					return putErr
				}
				summary.LastEventID = future.ID
			} // no proven rejected summary
			row.Body, e = json.Marshal(summary)
			if e != nil {
				return e
			}
			if e = tx.PutConnection(ctx, row); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	page, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: "today"})
	if err != nil || len(page.GetItems()) != 1 {
		t.Fatal(page, err)
	}
	detail, err := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: page.ReadToken, RowId: page.Items[0].Id})
	if err != nil || detail.GetRetainedConnectionCount() != 1 || len(detail.GetItems()) != 1 || detail.Items[0].Key != "legacy-reject/0" || detail.Summary.FailedCount != 5 {
		t.Fatal("legacy projection guessed missing evidence or changed totals", detail, err)
	}
	// Read projection never repairs storage or manufactures a recorded connection.
	if err = job.logs.Update(ctx, func(tx *historystore.LogsTx) error {
		row, ok, e := tx.Connection(ctx, "legacy-reject/0")
		if e != nil || !ok {
			return fmt.Errorf("summary missing: %w", e)
		}
		var summary config.LogConnectionSummary
		if e = json.Unmarshal(row.Body, &summary); e != nil {
			return e
		}
		if summary.Fact.Direction != "unknown" {
			t.Error("read mutated persisted history")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
