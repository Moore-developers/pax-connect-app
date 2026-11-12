package controller

import (
	"context"
	"fmt"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"testing"
	"time"
)

func TestAccessFiltersApplyBeforePagingAndShareDirectoryCounts(t *testing.T) {
	ctx := context.Background()
	c, p := newLogsTestController(t, newLogsTestDir(t))
	var e error
	start := time.Now().Add(-time.Hour)
	seq := uint64(0)
	for i := 0; i < 205; i++ {
		seq++
		at := start.Add(time.Duration(i) * time.Second)
		closed := at.Add(time.Second)
		f := config.FlowFact{Key: fmt.Sprintf("s/1/%d", i), Purpose: "access", Phase: "connecting", Domain: "api.x.com", Application: "Browser", ApplicationID: "browser", IP: "192.0.2.1", Port: 443, Transport: "tcp", Direction: "proxy", RouteID: "line", RouteName: "US", Rule: "final", StartedAt: at, ClosedAt: &closed, ResponseKnown: true, ResponseMS: 1500}
		if i < 100 {
			f.Domain = "other.test"
			f.ResponseMS = 20
		}
		if i == 204 {
			f.Failure = "connection_timeout"
		}
		p.feed(config.ObservationBatch{Session: "s", HighWater: seq, Collecting: true, Records: []config.Observation{{Session: "s", Sequence: seq, Kind: "closed", At: closed, Flow: &f}}})
	}
	q := &pb.LogQuery{View: "activity", Dimension: "domain", Quick: "slow", Limit: 100, Criteria: &pb.LogCriteria{Domain: "x.com", DomainMode: "suffix", Application: "brow", TargetIp: "192.0.2.0/24", Directions: []string{"proxy"}, Transports: []string{"tcp"}, Port: 443, Route: "US", RulePackage: "无"}}
	objects, e := c.QueryLogObjects(ctx, q)
	if e != nil || objects.TotalEvents != 105 || len(objects.Items) != 1 || objects.Items[0].Count != 105 {
		t.Fatal("directory does not reflect full filtered history", objects, e)
	}
	q.ReadToken = objects.ReadToken
	page, e := c.QueryLogs(ctx, q)
	if e != nil || page.Total != 105 || len(page.Records) != 100 || page.NextCursor == "" {
		t.Fatal("filtered after pagination", page, e)
	}
	if page.Records[0].Title != "建立连接：连接超时" {
		t.Fatal("failure stage lost", page.Records[0].Title)
	}
	q.Cursor = page.NextCursor
	last, e := c.QueryLogs(ctx, q)
	if e != nil || len(last.Records) != 5 || last.Total != 105 {
		t.Fatal("wrong next filtered page", last, e)
	}
	q.Criteria.DomainMode = "exact"
	q.Cursor = ""
	empty, e := c.QueryLogs(ctx, q)
	if e != nil || empty.Total != 0 {
		t.Fatal("exact domain matched subdomain", empty, e)
	}
	q.Criteria = &pb.LogCriteria{Kinds: []string{"dns"}, Directions: []string{"proxy"}}
	q.Quick = "all"
	empty, e = c.QueryLogs(ctx, q)
	if e != nil || empty.Total != 0 {
		t.Fatal("conflicting filters silently ignored", empty, e)
	}
	q.Criteria = &pb.LogCriteria{Domain: ".x.", DomainMode: "contains"}
	q.Cursor = ""
	keyword, e := c.QueryLogs(ctx, q)
	if e != nil || keyword.Total != 105 {
		t.Fatal("domain keyword rejected as hostname", keyword, e)
	}
	q.Criteria = &pb.LogCriteria{TargetIp: "invalid"}
	q.ReadToken = ""
	if _, e = c.QueryLogs(ctx, q); e == nil {
		t.Fatal("invalid IP filter accepted")
	}
}

func TestAccessPhaseProgressIsVisibleWithoutCompletingDial(t *testing.T) {
	ctx := context.Background()
	c, p := newLogsTestController(t, newLogsTestDir(t))
	var e error
	at := time.Now().Add(-time.Second)
	f := config.FlowFact{Key: "phase/1/c", Purpose: "access", Domain: "api.x.com", Phase: "routing", StartedAt: at}
	for i, kind := range []string{"started", "phase"} {
		if i == 1 {
			f.Phase = "connecting"
		}
		seq := uint64(i + 1)
		p.feed(config.ObservationBatch{Session: "phase", HighWater: seq, Collecting: true, Records: []config.Observation{{Session: "phase", Sequence: seq, At: at.Add(time.Duration(i) * time.Millisecond), Kind: kind, Flow: &f}}})
	}
	page, e := c.QueryLogs(ctx, &pb.LogQuery{View: "activity"})
	if e != nil || len(page.Records) != 1 || page.Records[0].Title != "建立连接：等待结果" {
		t.Fatal("dial stage not published to historical snapshot", page, e)
	}
}
