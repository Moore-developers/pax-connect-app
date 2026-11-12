package controller

import (
	"fmt"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"testing"
	"time"
)

// queryGoldenFixture keeps the full observation chain: several source kinds, routes,
// directions, a five-minute boundary and three calendar windows. testing/synctest
// pins time.Now, so the saved timestamps and partial last-bin seconds are exact.
func queryGoldenFixture(now time.Time) [][]config.Observation {
	var batches [][]config.Observation
	var seq uint64
	for _, days := range []int{14, 2, 0} {
		at := now.AddDate(0, 0, -days).Add(-10 * time.Minute).Truncate(5 * time.Minute).Add(-10 * time.Second)
		var records []config.Observation
		for i, kind := range []string{"app", "app", "system", "unknown", "unknown", "dns", "dns"} {
			f := config.FlowFact{Key: fmt.Sprintf("s/query/%d/%d", days, i), StartedAt: at, Domain: "api.example.com", IP: fmt.Sprintf("192.0.2.%d", i+1), RouteID: "route-a", RouteName: "Route A", Direction: "proxy", Transport: "tcp", Rule: "domain=example.com", RuleSource: "user", ResponseKnown: true, ResponseMS: uint64(20 + i)}
			switch kind {
			case "app":
				f.ApplicationID, f.Application = "fixture.app", "Fixture"
			case "system":
				f.Domain = "ocsp.example.net"
				f.Process = "/usr/libexec/trustd"
			case "unknown":
				f.Domain = "unknown.example.org"
			case "dns":
				f.Domain = "dns.example.edu"
				f.Purpose = "dns"
			}
			if i%2 == 1 {
				f.Direction, f.RouteID, f.RouteName = "direct", "direct", "直连"
			}
			if kind == "dns" {
				f.RouteName = "直连"
			}
			f.Rule = "domain=" + f.Domain
			for j, offset := range []time.Duration{time.Duration(i) * time.Second, 15*time.Second + time.Duration(i)*time.Second} {
				seq++
				k := "sample"
				if j == 0 {
					k = "started"
				}
				records = append(records, config.Observation{Session: "s", Sequence: seq, At: at.Add(offset), Kind: k, Flow: &f, Upload: uint64(i + 1), Download: uint64((i + 1) * 10)})
			}
		}
		batches = append(batches, records)
	}
	return batches
}
func queryGoldenPage(r *historyRead) *pb.TrafficPage {
	return &pb.TrafficPage{Items: r.rows, Upload: r.upload, Download: r.download, Connections: r.connections, Trend: r.trend, UnknownSource: r.unknown, DnsSummary: r.dns, SystemUpload: r.systemUpload, SystemDownload: r.systemDownload}
}

// queryGoldenTimezone keeps local-day cutoffs independent of the test host.
func queryGoldenTimezone(t *testing.T) {
	t.Helper()
	previous := time.Local
	time.Local = time.FixedZone("UTC+8", 8*60*60)
	t.Cleanup(func() { time.Local = previous })
}
