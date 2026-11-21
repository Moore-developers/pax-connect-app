package singbox

import (
	"testing"
	"time"

	"github.com/sagernet/sing-box/experimental/paxobs"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/telemetry"
)

func TestMatchedObservationRetainsMetadataWithoutOutboundTracker(t *testing.T) {
	o := newCoreObservations()
	at := time.Now()
	o.capture(paxobs.Event{ID: "dns", Stage: "accepted", At: at, Inbound: "tun", IP: "192.0.2.53", Port: 53})
	o.capture(paxobs.Event{ID: "dns", Stage: "matched", At: at, Network: "udp", Process: "/usr/sbin/mDNSResponder",
		Rule: `{"Package":"Geo 基础包","Method":"builtin","Target":"DNS 查询"}`})
	f := o.flows["dns"].fact
	if f.Process != "/usr/sbin/mDNSResponder" || f.Transport != "udp" || f.RuleSource != "Pax 包：Geo 基础包" {
		t.Fatalf("DNS rule bypass discarded available metadata: %+v", f)
	}
}

func TestUnavailableRouteIsRecordedOnlyAfterRejection(t *testing.T) {
	o := newCoreObservations()
	at := time.Now()
	label := `{"Package":"Claude","Method":"domain_suffix","Target":"claude.ai","UnavailableRoute":"A"}`
	o.capture(paxobs.Event{ID: "f", Stage: "accepted", At: at, Inbound: "mixed", Port: 443, Network: "tcp"})
	o.capture(paxobs.Event{ID: "f", Stage: "matched", At: at.Add(time.Millisecond), Rule: label, Domain: "claude.ai"})
	if f := o.flows["f"].fact; f.Rule != "domain_suffix=claude.ai" || f.Direction != "unknown" {
		t.Fatalf("match alone recorded a rejection: %+v", f)
	}
	o.capture(paxobs.Event{ID: "f", Stage: "rejected", At: at.Add(2 * time.Millisecond), Level: "error"})
	o.capture(paxobs.Event{ID: "f", Stage: "closed", At: at.Add(3 * time.Millisecond)})
	for _, observation := range o.records {
		f := *observation.Flow
		if f.Direction != "blocked" || f.RouteID != "" || f.RouteName != "" || f.Failure != "rule_rejected" {
			t.Fatalf("rejection attributed to an actual outbound: %+v", f)
		}
		if f.Rule != label || f.RuleSource != "Pax 包：Claude" || config.LogStatus(f) != "指定线路 A 不可用" {
			t.Fatalf("missing recorded rule or rejection reason: %+v", f)
		}
	}
	if len(o.records) != 2 {
		t.Fatalf("wanted rejection and close, got %+v", o.records)
	}
	if event := telemetry.ProductEvent(o.records[0]); event == nil || event.Title != "指定线路 A 不可用" {
		t.Fatalf("missing named product event: %+v", event)
	}
}

func TestProxyEndpointIsNotInventedAsDestination(t *testing.T) {
	o := newCoreObservations()
	at := time.Now()
	o.capture(paxobs.Event{ID: "p", Stage: "accepted", At: at, Inbound: "mixed", Domain: "example.test", Port: 443, Network: "tcp"})
	o.capture(paxobs.Event{ID: "p", Stage: "routed", At: at, Domain: "example.test", Port: 443, Network: "tcp", Outbound: "proxy"})
	f := o.flows["p"].fact
	if f.IP != "" || f.IPSource != "proxy_domain" {
		t.Fatal("proxy did not expose target IP", f)
	}
}
