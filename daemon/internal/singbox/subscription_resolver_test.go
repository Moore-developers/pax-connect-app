package singbox

import (
	"github.com/miekg/dns"
	"net"
	"testing"
)

func TestSubscriptionDNSAcceptsOnlyMatchingIPv4Response(t *testing.T) {
	query := new(dns.Msg).SetQuestion("fixture.test.", dns.TypeA)
	response := new(dns.Msg).SetReply(query)
	response.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: "fixture.test.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.ParseIP("192.0.2.1")}}
	wire, err := response.Pack()
	if err != nil {
		t.Fatal(err)
	}
	addresses, err := subscriptionIPv4Answers(query, wire)
	if err != nil || len(addresses) != 1 || !addresses[0].Is4() {
		t.Fatal(addresses, err)
	}
	response.Id++
	wire, err = response.Pack()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := subscriptionIPv4Answers(query, wire); err != errSubscriptionDNS {
		t.Fatal("mismatched response accepted", err)
	}
	response.SetReply(query)
	response.Answer = nil
	wire, err = response.Pack()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := subscriptionIPv4Answers(query, wire); err != errSubscriptionDNS {
		t.Fatal("missing IPv4 accepted", err)
	}
}
