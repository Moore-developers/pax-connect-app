package singbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/miekg/dns"
	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
)

// Subscription fetching owns an isolated, frozen outbound. Its bootstrap must
// not use the running proxy DNS router; this explicit path is separate from
// user-traffic resolution, which shares the fenced kernel TTL cache.
var errSubscriptionDNS = errors.New("subscription_dns_failed")

func routeNetworkResolver(backend adapter.Outbound) (func(context.Context, string) ([]netip.Addr, error), func()) {
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return backend.DialContext(ctx, network, M.ParseSocksaddr(address))
	}}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(ctx context.Context, domain string) ([]netip.Addr, error) {
		query := new(dns.Msg).SetQuestion(dns.Fqdn(domain), dns.TypeA)
		payload, err := query.Pack()
		if err != nil {
			return nil, errSubscriptionDNS
		}

		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+remoteDNSServer+"/dns-query", bytes.NewReader(payload))
		if err != nil {
			return nil, errSubscriptionDNS
		}
		request.Header.Set("Content-Type", "application/dns-message")
		request.Header.Set("Accept", "application/dns-message")
		response, err := client.Do(request)
		if err != nil {
			return nil, errSubscriptionDNS
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != http.StatusOK {
			return nil, errSubscriptionDNS
		}
		wire, err := io.ReadAll(io.LimitReader(response.Body, 65537))
		if err != nil || len(wire) > 65536 {
			return nil, errSubscriptionDNS
		}
		return subscriptionIPv4Answers(query, wire)
	}, transport.CloseIdleConnections
}
func subscriptionIPv4Answers(query *dns.Msg, wire []byte) ([]netip.Addr, error) {
	var response dns.Msg
	if err := response.Unpack(wire); err != nil || !response.Response || response.Id != query.Id || response.Rcode != dns.RcodeSuccess || response.Truncated || len(response.Question) != 1 || response.Question[0] != query.Question[0] {
		return nil, errSubscriptionDNS
	}
	var addresses []netip.Addr
	for _, answer := range response.Answer {
		if record, ok := answer.(*dns.A); ok {
			if address, ok := netip.AddrFromSlice(record.A); ok {
				addresses = append(addresses, address.Unmap())
			}
		}
	}
	if len(addresses) == 0 {
		return nil, errSubscriptionDNS
	}
	return addresses, nil
}
