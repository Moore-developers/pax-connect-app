package controller

import (
	"context"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"testing"
	"time"
)

func TestDNSFactsPersistAndStaySeparateFromAccessRankings(t *testing.T) {
	ctx := context.Background()
	dir := newLogsTestDir(t)
	c, p := newLogsTestController(t, dir)
	start := time.Now().Add(-time.Minute)
	closed := start.Add(time.Second)
	dns := config.FlowFact{Key: "s/1/dns", Purpose: "dns", Domain: "api.example.test", Transport: "udp", Direction: "dns", StartedAt: start, ClosedAt: &closed, ResponseKnown: true, ResponseMS: 12, ProcessStatus: "not_found", DNS: &config.DNSFact{QueryType: "A", Result: "NOERROR", Answers: []string{"192.0.2.1"}}}
	bad := dns
	bad.Key = "s/1/bad"
	bad.Domain = "missing.test"
	bad.Failure = "dns_not_found"
	bad.DNS = &config.DNSFact{QueryType: "A", Result: "NXDOMAIN"}
	access := config.FlowFact{Key: "s/1/access", Purpose: "access", Domain: "api.example.test", IP: "192.0.2.1", IPSource: "connected", Direction: "direct", StartedAt: start, ClosedAt: &closed}
	for i, fact := range []config.FlowFact{dns, bad, access} {
		seq := uint64(2*i + 2)
		kind := "closed"
		if fact.Failure != "" {
			kind = "failed"
		}
		p.observe(config.ObservationBatch{Session: "s", HighWater: seq, Collecting: true, Records: []config.Observation{
			{Session: "s", Sequence: seq - 1, At: start, Kind: "started", Flow: &fact},
			{Session: "s", Sequence: seq, At: closed, Kind: kind, Flow: &fact},
		}})
	}
	check := func(c *Controller) {
		t.Helper()
		page, e := c.QueryLogs(ctx, &pb.LogQuery{View: "activity"})
		if e != nil || len(page.Records) != 2 {
			t.Fatalf("default must hide normal DNS, keep failures and access: records=%d err=%v", len(page.GetRecords()), e)
		}
		all, e := c.QueryLogs(ctx, &pb.LogQuery{View: "activity", IncludeDns: true})
		if e != nil || len(all.Records) != 3 {
			t.Fatal("DNS toggle did not restore history", e)
		}
		objects, e := c.QueryLogObjects(ctx, &pb.LogQuery{View: "activity", Dimension: "domain"})
		if e != nil || objects.TotalEvents != 2 {
			t.Fatal("directory counts disagree with default query", objects, e)
		}
		linked, e := c.QueryLogs(ctx, &pb.LogQuery{View: "activity", ConnectionKey: "s/1/dns"})
		if e != nil || len(linked.Records) != 1 || linked.Records[0].Flow.Dns.GetAnswers()[0] != "192.0.2.1" || linked.Records[0].Flow.ProcessStatus != "not_found" {
			t.Fatal("lost DNS facts or connection filter", linked, e)
		}
		traffic, e := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: "day", Direction: "all"})
		if e != nil || traffic.Connections != 0 {
			t.Fatal("DNS polluted traffic header/ranking", traffic, e)
		}
		if len(traffic.Items) != 0 || traffic.UnknownSource == nil || traffic.UnknownSource.Connections != 1 {
			t.Fatal("ordinary unattributed access was hidden")
		}
	}
	check(c)
	stopLogsTestController(t, c)
	restored, _ := newLogsTestController(t, dir)
	check(restored)
}
