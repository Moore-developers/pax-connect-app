package singbox

import (
	"context"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/sagernet/sing-box/experimental/paxobs"
	"pax-connect/daemon/internal/config"
)

func TestDNSPathProofRequiresResponseAndTracksBackup(t *testing.T) {
	first, second := &fakePrivacyDNS{}, &fakePrivacyDNS{}
	pool := &encryptedPool{primary: first, backup: second, pool: "overseas", outbound: "proxy", primaryServer: "1.1.1.1", backupServer: "8.8.8.8"}
	query := new(dns.Msg)
	query.SetQuestion("test.fixture.test.", dns.TypeA)
	var events []paxobs.Event
	ctx := paxobs.WithSink(context.Background(), func(e paxobs.Event) {
		if e.Stage == "dns_upstream" {
			events = append(events, e)
		}
	})
	ctx, _ = paxobs.BeginDNS(ctx, query, "udp", "tun", "")
	if _, err := pool.Exchange(ctx, query); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].IP != "1.1.1.1" || events[0].Outbound != "proxy" {
		t.Fatal(events)
	}
	first.fail = true
	if _, err := pool.Exchange(ctx, query); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].IP != "8.8.8.8" {
		t.Fatal(events)
	}
	if _, err := pool.Exchange(ctx, query); err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[2].IP != "8.8.8.8" {
		t.Fatal("cooldown proof", events)
	}
	second.fail = true
	if _, err := pool.Exchange(ctx, query); err == nil {
		t.Fatal("expected failure")
	}
	if len(events) != 3 {
		t.Fatal("failed exchange produced proof")
	}
	first.fail = false
	first.rcode = dns.RcodeNameError
	pool.primaryRetry = time.Time{}
	if _, err := pool.Exchange(ctx, query); err != nil || len(events) != 4 {
		t.Fatal("NXDOMAIN still proves a transport response", err, events)
	}
}
func TestDNSPathObservationRetainsProofWithoutChangingDNSDirection(t *testing.T) {
	o := newCoreObservations()
	at := time.Now()
	o.capture(paxobs.Event{ID: "q", Stage: "accepted", At: at, Inbound: "tun", Purpose: "dns", Domain: "test.fixture.test", QueryType: "A"})
	original := o.flows["q"].fact
	o.capture(paxobs.Event{ID: "q", Stage: "dns_upstream", At: at, IP: "1.1.1.1", Outbound: "proxy", Rule: "overseas"})
	o.capture(paxobs.Event{ID: "q", Stage: "dns_response", At: at, DNSResult: "NOERROR"})
	fact := o.flows["q"].fact
	if fact.DNS.Upstream != "1.1.1.1" || fact.DNS.Outbound != "proxy" || fact.Direction != "dns" || fact.Purpose != "dns" {
		t.Fatal(fact)
	}
	if original.DNS.Upstream != "" {
		t.Fatal("mutated published snapshot")
	}
	// Old facts without optional proof and non-DNS traffic must remain unknown.
	if (config.DNSFact{}).Outbound != "" {
		t.Fatal("default proof")
	}
	o.capture(paxobs.Event{ID: "access", Stage: "accepted", At: at, Inbound: "tun"})
	before := len(o.records)
	o.capture(paxobs.Event{ID: "access", Stage: "dns_upstream", At: at, IP: "1.1.1.1", Outbound: "proxy"})
	if o.flows["access"].fact.DNS != nil || len(o.records) != before {
		t.Fatal("access flow accepted DNS proof")
	}
}
