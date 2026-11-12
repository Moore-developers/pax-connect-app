package singbox

import (
	"github.com/sagernet/sing-box/experimental/paxobs"
	"testing"
	"time"
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
