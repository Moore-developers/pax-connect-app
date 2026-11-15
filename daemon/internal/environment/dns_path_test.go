package environment

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func dnsFixture() ([]string, []DNSFlow, time.Time, time.Time) {
	from := time.Now().Add(-time.Second)
	to := time.Now().Add(time.Second)
	names := dnsProbeNames("fixture123")
	flows := make([]DNSFlow, 0, len(names))
	for _, name := range names {
		flows = append(flows, DNSFlow{Domain: name, StartedAt: from, UpstreamAt: from.Add(time.Millisecond), Upstream: "1.1.1.1", Outbound: "proxy", Pool: "overseas"})
	}
	return names, flows, from, to
}
func TestDNSPathExactCoverageAndPolicy(t *testing.T) {
	names, flows, from, to := dnsFixture()
	for _, scenario := range []struct{ state, pool, server, outbound string }{{"proxy", "overseas", "1.1.1.1", "proxy"}, {"policy_direct", "domestic", "223.5.5.5", "direct"}, {"bypass", "overseas", "8.8.8.8", "direct"}} {
		for i := range flows {
			flows[i].Pool, flows[i].Upstream, flows[i].Outbound = scenario.pool, scenario.server, scenario.outbound
		}
		got := DNSPathChecks(names, flows, true, from, to)
		if got[0].State != scenario.state || len(got) != 2 {
			t.Fatalf("%s: %+v", scenario.state, got)
		}
		raw, _ := json.Marshal(got)
		if strings.Contains(string(raw), "fixture123") || strings.Contains(string(raw), names[0]) {
			t.Fatal("test token exported")
		}
	}
	names, flows, from, to = dnsFixture()
	flows[0].Pool, flows[0].Upstream, flows[0].Outbound = "domestic", "119.29.29.29", "direct"
	if got := DNSPathChecks(names, flows, true, from, to); got[0].State != "mixed" {
		t.Fatal(got)
	}
}
func TestDNSPathNeverConfirmsPartialCachedForeignOrOldQueries(t *testing.T) {
	names, flows, from, to := dnsFixture()
	if got := DNSPathChecks(names, flows[:11], true, from, to); got[0].State != "partial" {
		t.Fatal(got)
	}
	// Even a second query for a covered name without a transport response stays uncertain.
	flows = append(flows, DNSFlow{Domain: names[0], StartedAt: from})
	if got := DNSPathChecks(names, flows, true, from, to); got[0].State != "partial" {
		t.Fatal(got)
	}
	_, flows, _, _ = dnsFixture()
	for i := range flows {
		flows[i].Domain = "other." + names[0]
	}
	if got := DNSPathChecks(names, flows, true, from, to); got[0].State != "unobserved" {
		t.Fatal(got)
	}
	_, flows, _, _ = dnsFixture()
	for i := range flows {
		flows[i].Domain = names[i]
		flows[i].StartedAt = from.Add(-time.Second)
	}
	if got := DNSPathChecks(names, flows, true, from, to); got[0].State != "unobserved" {
		t.Fatal(got)
	}
	if got := DNSPathChecks(names, nil, false, from, to); got[0].State != "unavailable" {
		t.Fatal(got)
	}
}

type dnsTestProvider struct {
	fakeProvider
	names []string
	flows []DNSFlow
}

func (f *dnsTestProvider) DNSPath(names []string, from, to time.Time) []Check {
	return DNSPathChecks(names, f.flows, true, from, to)
}
func TestDNSPathCollectionCancellation(t *testing.T) {
	names, flows, from, to := dnsFixture()
	p := &dnsTestProvider{names: names, flows: flows}
	if got := collectDNSPath(context.Background(), p, names, from, to); got[0].State != "proxy" {
		t.Fatal(got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := collectDNSPath(ctx, p, names, from, to); got[0].State == "proxy" {
		t.Fatal("cancelled run confirmed", got)
	}
}

func TestDNSPathRemoteDelegationRequiresProxyResponseAndExactNames(t *testing.T) {
	names, flows, from, to := dnsFixture()
	for i := range flows {
		flows[i].Upstream = ""
		flows[i].Pool = "remote"
		flows[i].RemoteProxy, flows[i].Responded = true, true
		flows[i].RemoteRoute = "Fixture Route"
	}
	got := DNSPathChecks(names, flows, true, from, to)
	if got[0].State != "proxy" || len(got) != 2 || got[1].State != "remote_proxy" || !strings.Contains(got[1].Value, "Fixture Route") {
		t.Fatal(got)
	}
	flows[0].Responded = false
	if got := DNSPathChecks(names, flows, true, from, to); got[0].State != "partial" {
		t.Fatal("unanswered connection counted", got)
	}
	flows[0].Responded = true
	flows[0].RemoteProxy = false
	if got := DNSPathChecks(names, flows, true, from, to); got[0].State != "partial" {
		t.Fatal("non-proxy domain counted", got)
	}
	flows[0].RemoteProxy = true
	flows[0].Domain = "other." + names[0]
	if got := DNSPathChecks(names, flows, true, from, to); got[0].State != "partial" {
		t.Fatal("suffix lookalike counted", got)
	}
}
