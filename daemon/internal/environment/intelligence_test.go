package environment

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"pax-connect/daemon/internal/measurement"
)

func TestEnvironmentSameIPIntelligenceSharedWithinRound(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	client := fixtureClient(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		calls[r.URL.Host]++
		mu.Unlock()
		body := `{"ip":"8.8.8.8","country_code":"US","connection":{"asn":15169}}`
		if r.URL.Host == "api.ipquery.io" {
			body = `{"ip":"8.8.8.8","isp":{"asn":"AS15169"},"location":{"country_code":"US"},"risk":{"is_proxy":false,"is_vpn":false,"is_tor":false,"is_datacenter":true,"is_mobile":false}}`
		}
		if r.URL.Host == "internetdb.shodan.io" {
			body = `{"ip":"8.8.8.8","ports":[],"tags":[],"vulns":[]}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	s := &Session{client: client, lookup: measurement.NewExitLookup(), state: Snapshot{ID: "round"}}
	a, b := Sample{IPv4: "8.8.8.8"}, Sample{IPv4: "8.8.8.8"}
	var workers sync.WaitGroup
	workers.Add(2)
	go func() { defer workers.Done(); s.enrich(context.Background(), &a) }()
	go func() { defer workers.Done(); s.enrich(context.Background(), &b) }()
	workers.Wait()
	if len(a.Intelligence.Providers) != 4 || len(b.Intelligence.Providers) != 4 || calls["api.ipquery.io"] != 1 || calls["ipwho.is"] != 1 || calls["internetdb.shodan.io"] != 1 || calls["ipx.al"] != 1 {
		t.Fatal(calls, a.Intelligence, b.Intelligence)
	}
}
