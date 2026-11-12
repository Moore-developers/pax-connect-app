package controller_test

import (
	"context"
	"fmt"
	"google.golang.org/protobuf/proto"
	"os"
	"path/filepath"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/controller"
	"testing"
	"time"
)

func TestDNSSummaryIgnoresDirectionAndGroupsBeforePaging(t *testing.T) {
	ctx := context.Background()
	dir, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	c := controller.New("test", "test")
	if e = controller.ConfigureHistoryFixture(t, c, ctx, dir, uint32(os.Geteuid())); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = controller.CloseHistoryFixture(c, ctx) }()
	at := time.Now().Add(-time.Minute)
	var records []config.Observation
	for i := 0; i < 150; i++ {
		result := "NOERROR"
		domain := "api.example.test"
		if i >= 100 {
			result = "NXDOMAIN"
			domain = "missing.test"
		}
		f := config.FlowFact{Key: fmt.Sprintf("dns/1/%d", i), Purpose: "dns", Domain: domain, DNS: &config.DNSFact{QueryType: "A", Result: result, Answers: []string{"192.0.2.1"}}, StartedAt: at.Add(time.Duration(i) * time.Millisecond), ResponseKnown: true, ResponseMS: 12}
		if i >= 100 {
			f.Failure = "dns_not_found"
		}
		end := f.StartedAt.Add(time.Millisecond)
		f.ClosedAt = &end
		records = append(records,
			config.Observation{Session: "dns", Sequence: uint64(2*i + 1), Kind: "started", At: f.StartedAt, Flow: &f},
			config.Observation{Session: "dns", Sequence: uint64(2*i + 2), Kind: "closed", At: end, Flow: &f})
	}
	if e = controller.FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "dns", HighWater: 300, Collecting: true, Records: records}); e != nil {
		t.Fatal(e)
	}
	p, e := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "system", Direction: "proxy", Window: "day"})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Items) != 0 || p.Connections != 0 || p.DnsSummary == nil || p.DnsSummary.Connections != 150 || p.DnsSummary.FailedCount != 50 || p.DnsSummary.ValidLatencyCount != 150 {
		t.Fatal("DNS summary mixed into connection table or obeyed direction", p)
	}
	d, e := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{RowId: p.DnsSummary.Id, ReadToken: p.ReadToken, Limit: 1})
	if e != nil || d.DnsGroupTotal != 2 || len(d.DnsGroups) != 1 || len(d.Items) != 0 || d.NextCursor == "" {
		t.Fatal("DNS grouping did not precede paging", d, e)
	}
	group := d.DnsGroups[0]
	r, e := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{RowId: p.DnsSummary.Id, ReadToken: p.ReadToken, DnsGroupId: group.Id, Limit: 2})
	if e != nil || len(r.Items) != 2 || r.NextCursor == "" {
		t.Fatal("group records unavailable", r, e)
	}
	if len(d.RecentResponses) != 60 || len(p.DnsSummary.RecentResponses) != 20 || len(r.RecentResponses) != 50 {
		t.Fatalf("DNS series size total=%d list=%d group=%d", len(d.RecentResponses), len(p.DnsSummary.RecentResponses), len(r.RecentResponses))
	}
	for _, point := range r.RecentResponses {
		if point.Milliseconds != 12 || point.StartedAt.AsTime().Before(at.Add(100*time.Millisecond)) {
			t.Fatal("DNS group received total-row samples")
		}
	}
	for _, f := range r.Items {
		if f.Domain != group.Domain || f.Dns.Result != group.Result {
			t.Fatal("unrelated DNS queries leaked")
		}
	}
	if _, e = c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{RowId: p.DnsSummary.Id, ReadToken: p.ReadToken, Cursor: r.NextCursor, Limit: 2}); e == nil {
		t.Fatal("record cursor reused as group cursor")
	}
	search, e := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "system", Direction: "direct", Window: "day", Search: "missing.test"})
	if e != nil || search.DnsSummary == nil || search.DnsSummary.Connections != 150 {
		t.Fatal("DNS search must locate the complete summary", search, e)
	}
	if !proto.Equal(p.DnsSummary, search.DnsSummary) {
		t.Fatal("search changed DNS complete counters or latency")
	}
	complete, e := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{RowId: search.DnsSummary.Id, ReadToken: search.ReadToken})
	if e != nil || complete.DnsGroupTotal != 2 || len(complete.DnsGroups) != 2 {
		t.Fatal("search clipped DNS detail groups", e)
	}
}
