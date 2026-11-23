package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// Rankings and filters must account for retained records beyond the old 20-row page.
func TestTrafficConnectionGroupsUseFullFrozenPool(t *testing.T) {
	c, _ := newTrafficRPCFixture(t)
	ctx := context.Background()
	at := time.Now().Add(-10 * time.Minute)
	var records []config.Observation
	for i := 0; i < 46; i++ {
		host := "api.example.test"
		if i >= 25 {
			host = fmt.Sprintf("target-%02d.example.test", i)
		}
		if i == 45 {
			host = ""
		}
		f := config.FlowFact{Key: fmt.Sprintf("groups/%02d", i), ApplicationID: "fixture.app", Application: "Fixture", Domain: host, IP: "203.0.113.8", Port: 443, Transport: "tcp", Process: "/Applications/Fixture.app/Contents/MacOS/Fixture", ResponseKnown: true, ResponseMS: uint64(i), StartedAt: at.Add(time.Duration(i) * time.Second), Direction: "proxy", RouteID: "a", RouteName: "A", Rule: "domain_suffix=example.test", RuleSource: "Pax 包：专线"}
		if i%2 == 0 {
			f.Rule = "final"
			f.RuleSource = ""
		}
		records = append(records, config.Observation{Session: "groups", Sequence: uint64(i + 1), At: f.StartedAt, Kind: "started", Flow: &f, Upload: 10, Download: 20})
	}
	if err := FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "groups", HighWater: 46, Records: records}); err != nil {
		t.Fatal(err)
	}
	for _, window := range []string{"day", "today", "week"} {
		t.Run(window, func(t *testing.T) {
			page, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: window})
			if err != nil || len(page.GetItems()) != 1 {
				t.Fatal(page, err)
			}
			q := &pb.TrafficDetailRequest{RowId: page.Items[0].Id, ReadToken: page.ReadToken, GroupConnections: true, Limit: 20}
			first, err := c.QueryTrafficDetail(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			if first.TargetGroupTotal != 22 || first.FilteredConnectionCount != 46 || len(first.ConnectionGroups) != 20 || first.NextCursor == "" {
				t.Fatalf("wrong full-pool count/page: %v", first)
			}
			top := first.ConnectionGroups[0]
			if top.Target != "api.example.test" || top.Connections != 25 || top.Upload != 250 || top.Download != 500 || len(top.Rules) != 2 || len(top.Exits) != 1 || top.BytesState != "complete" {
				t.Fatalf("bad top aggregate: %v", top)
			}
			if top.ValidLatencyCount != 25 || len(top.RecentResponses) != 25 || top.RecentResponses[0].Milliseconds != 0 || top.RecentResponses[24].Milliseconds != 24 {
				t.Fatal("full-target response evidence missing or fabricated", top)
			}
			if len(top.Sources) != 1 || top.Sources[0].Process != "/Applications/Fixture.app/Contents/MacOS/Fixture" || top.Sources[0].Connections != 25 || len(top.Addresses) != 1 || top.Addresses[0].Transport != "tcp" || top.Addresses[0].Connections != 25 || len(top.Results) != 1 || top.Results[0].Connections != 25 {
				t.Fatal("source/address/result evidence does not match full target", top)
			}
			q.ReadToken = first.ReadToken
			q.Cursor = first.NextCursor
			next, err := c.QueryTrafficDetail(ctx, q)
			if err != nil || len(next.GetConnectionGroups()) != 2 || next.NextCursor != "" {
				t.Fatal("group pagination", next, err)
			}
			q.Cursor = ""
			q.TargetGroupId = top.Id
			members, err := c.QueryTrafficDetail(ctx, q)
			if err != nil || len(members.GetItems()) != 20 || members.NextCursor == "" {
				t.Fatal("group members", members, err)
			}
			q.Cursor = members.NextCursor
			tail, err := c.QueryTrafficDetail(ctx, q)
			if err != nil || len(tail.GetItems()) != 5 || tail.NextCursor != "" {
				t.Fatal("group member tail", tail, err)
			}
			for _, v := range append(members.Items, tail.Items...) {
				if v.Domain != "api.example.test" {
					t.Fatal("cross-target member", v)
				}
			}
			q.Cursor = ""
			q.TargetGroupId = ""
			q.ConnectionSearch = "API.EXAMPLE"
			q.ConnectionExit = "proxy\ta\tA"
			q.ConnectionRule = connectionRuleIdentity("", "final")
			filtered, err := c.QueryTrafficDetail(ctx, q)
			if err != nil || filtered.TargetGroupTotal != 1 || filtered.FilteredConnectionCount != 13 || filtered.ConnectionGroups[0].Upload != 130 || filtered.ConnectionGroups[0].Rules[0].Connections != 13 {
				t.Fatal("intersection filter failed", filtered, err)
			}
			if len(filtered.ConnectionRules) != 2 || len(filtered.ConnectionExits) != 1 {
				t.Fatal("filter choices lost")
			}
			q.ConnectionRule = "does not exist"
			empty, err := c.QueryTrafficDetail(ctx, q)
			if err != nil || empty.FilteredConnectionCount != 0 || len(empty.ConnectionGroups) != 0 {
				t.Fatal("empty filter fabricated data", empty, err)
			}
			// A group cursor cannot silently become a member or differently filtered cursor.
			q.Cursor = first.NextCursor
			if _, err = c.QueryTrafficDetail(ctx, q); err == nil {
				t.Fatal("cursor crossed filter identity")
			}
		})
	}
	// Opening a detail freezes records even if the live database is cleared.
	page, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: "today"})
	if err != nil {
		t.Fatal(err)
	}
	q := &pb.TrafficDetailRequest{RowId: page.Items[0].Id, ReadToken: page.ReadToken, GroupConnections: true}
	before, err := c.QueryTrafficDetail(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	q.ReadToken = before.ReadToken
	logs := stopWriterGoroutine(t, c)
	if err = logs.logs.Update(ctx, func(tx *historystore.LogsTx) error { return tx.ClearLogTables(ctx) }); err != nil {
		t.Fatal(err)
	}
	after, err := c.QueryTrafficDetail(ctx, q)
	if err != nil || !proto.Equal(before, after) {
		t.Fatal("opened groups changed after clear", err)
	}
}

func TestTrafficConnectionGroupUnknownTargets(t *testing.T) {
	c, _ := newTrafficRPCFixture(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Minute)
	f := config.FlowFact{Key: "unknown/1", ApplicationID: "fixture.app", Application: "Fixture", StartedAt: at, Direction: "blocked", Failure: "rule_rejected", Rule: "final"}
	if err := FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "unknown", HighWater: 1, Records: []config.Observation{{Session: "unknown", Sequence: 1, At: at, Kind: "rejected", Flow: &f}}}); err != nil {
		t.Fatal(err)
	}
	page, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: "today"})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{RowId: page.Items[0].Id, ReadToken: page.ReadToken, GroupConnections: true})
	if err != nil || len(detail.GetConnectionGroups()) != 1 || detail.ConnectionGroups[0].Kind != "unknown" || detail.ConnectionGroups[0].Target != "" {
		t.Fatal(detail, err)
	}
	direct, err := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ConnectionKey: f.Key, GroupConnections: true})
	if err != nil || len(direct.GetConnectionGroups()) != 1 || direct.ConnectionGroups[0].Kind != "unknown" {
		t.Fatal("direct log entry lost unknown target", direct, err)
	}
	members, err := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: direct.ReadToken, ConnectionKey: f.Key, TargetGroupId: direct.ConnectionGroups[0].Id, GroupConnections: true})
	if err != nil || len(members.GetItems()) != 1 || members.Items[0].Key != f.Key {
		t.Fatal("direct log member expansion", members, err)
	}

}

// Evidence quality must survive full-target byte aggregation.
func TestTrafficConnectionGroupByteQuality(t *testing.T) {
	for _, scenario := range []struct {
		states []string
		want   string
	}{
		{[]string{"complete", "complete"}, "complete"},
		{[]string{"complete", "unknown"}, "partial"},
		{[]string{"partial", "complete"}, "partial"},
		{[]string{"unknown", "unknown"}, "unknown"},
	} {
		t.Run(scenario.want+fmt.Sprint(scenario.states), func(t *testing.T) {
			c, _ := newTrafficRPCFixture(t)
			ctx := context.Background()
			at := time.Now().Add(-time.Minute)
			var observations []config.Observation
			for i := range scenario.states {
				f := config.FlowFact{Key: fmt.Sprintf("bytes/%d", i), ApplicationID: "fixture.bytes", Application: "Bytes", Domain: "bytes.example.test", StartedAt: at.Add(time.Duration(i) * time.Second), Direction: "direct"}
				observations = append(observations, config.Observation{Session: "bytes", Sequence: uint64(i + 1), At: f.StartedAt, Kind: "started", Flow: &f, Upload: 10, Download: 20})
			}
			if err := FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "bytes", HighWater: 2, Records: observations}); err != nil {
				t.Fatal(err)
			}
			logs := stopWriterGoroutine(t, c)
			if err := logs.logs.Update(ctx, func(tx *historystore.LogsTx) error {
				for i, state := range scenario.states {
					v, ok, err := tx.Summary(ctx, fmt.Sprintf("bytes/%d", i))
					if err != nil {
						return err
					}
					if !ok {
						return fmt.Errorf("missing fixture")
					}
					v.Metrics.BytesState = state
					if state == "unknown" {
						v.Metrics.Upload = 0
						v.Metrics.Download = 0
					}
					if err = tx.PutSummary(ctx, v); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			page, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: "day"})
			if err != nil {
				t.Fatal(err)
			}
			detail, err := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: page.ReadToken, RowId: page.Items[0].Id, GroupConnections: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(detail.ConnectionGroups) != 1 || detail.ConnectionGroups[0].BytesState != scenario.want {
				t.Fatal("incorrect quality", detail)
			}
		})
	}
}
