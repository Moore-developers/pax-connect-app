package controller_test

import (
	"context"
	"os"
	"path/filepath"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/controller"
	"testing"
	"time"
)

func TestSystemTrafficUsesOnlyDNSAndObservedSystemProcesses(t *testing.T) {
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
	now := time.Now().Add(-time.Minute)
	fixtures := []config.FlowFact{
		{Key: "s/1/dns", Purpose: "dns", Domain: "query.test", DNS: &config.DNSFact{QueryType: "A", Result: "NOERROR", Answers: []string{"192.0.2.2"}}, ResponseKnown: true, ResponseMS: 12},
		{Key: "s/1/dns-failed", Purpose: "dns", Domain: "missing.test", DNS: &config.DNSFact{QueryType: "A", Result: "NXDOMAIN"}, ResponseKnown: true, ResponseMS: 22, Failure: "dns_not_found", EstablishmentState: "failed"},
		{Key: "s/1/service", Process: "/usr/libexec/trustd", ApplicationID: "process:/usr/libexec/trustd", Application: "trustd", Direction: "direct", RouteID: "direct", ResponseKnown: true, ResponseMS: 35},
		{Key: "s/1/app", Process: "/System/Applications/Safari.app/Contents/MacOS/Safari", ApplicationID: "com.apple.Safari", Application: "Safari", Direction: "direct"},
		{Key: "s/1/unknown", Direction: "direct"},
		{Key: "s/1/tool", Process: "/usr/bin/curl", ApplicationID: "process:/usr/bin/curl", Application: "curl", Direction: "direct"},
	}
	var records []config.Observation
	for i, f := range fixtures {
		f.StartedAt = now
		end := now.Add(time.Second)
		f.ClosedAt = &end
		records = append(records, config.Observation{Session: "s", Sequence: uint64(i + 1), Kind: "started", At: end, Flow: &f, Upload: 10, Download: 20})
	}
	if e = controller.FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "s", HighWater: 6, Collecting: true, Records: records}); e != nil {
		t.Fatal(e)
	}
	page, e := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "system", Window: "day"})
	if e != nil {
		t.Fatal(e)
	}

	if page.Connections != 2 || len(page.Items) != 2 || page.DnsSummary == nil {
		t.Fatal("system services and DNS not separated", page)
	}
	row := page.DnsSummary
	if row.Connections != 2 || row.ValidLatencyCount != 2 || row.FailedCount != 1 || row.BytesKnown || row.Upload != 0 || row.Download != 0 {
		t.Fatal("DNS summary lost typed evidence", row)
	}
	d, e := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: page.ReadToken, RowId: row.Id})
	if e != nil || len(d.DnsGroups) != 2 || len(d.Items) != 0 {
		t.Fatal("DNS grouping absent", d, e)
	}

	app, e := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: "day"})
	if e != nil {
		t.Fatal(e)
	}
	if app.Connections != 1 || app.OtherProcessConnections != 0 || app.UnknownSource == nil || app.UnknownSource.Connections != 1 {
		t.Fatal("existing application access semantics changed", app)
	}
	today, e := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "system", Window: "today"})
	if e != nil || today.Upload != 20 || today.Download != 40 {
		t.Fatal("system today borrowed unrelated traffic", today, e)
	}
	proxy, e := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "system", Direction: "proxy", Window: "day"})
	if e != nil || proxy.Connections != 0 {
		t.Fatal("invented DNS outbound", proxy, e)
	}
}
