package measurement

import (
	"context"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

const independentIP = "203.0.113.24"
const independentGeo = `{"ip":"203.0.113.24","country_code":"US","city":"Other City","connection":{"asn":64500,"isp":"Example"}}`
const independentQuery = `{"ip":"203.0.113.24","isp":{"asn":"AS64500","isp":"Example"},"location":{"country_code":"US","city":"City","timezone":"America/Los_Angeles"},"risk":{"is_proxy":false,"is_vpn":false,"is_tor":false,"is_datacenter":false,"is_mobile":false,"risk_score":0}}`
const independentDB = `{"ip":"203.0.113.24","ports":[],"vulns":[],"tags":[]}`

type intelligenceFixture struct {
	mu       sync.Mutex
	calls    map[string]int
	response func(*http.Request) (int, string)
}

func (f *intelligenceFixture) Do(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.calls[r.URL.Host]++
	f.mu.Unlock()
	status, body := f.response(r)
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
}
func intelligenceDefault(r *http.Request) (int, string) {
	switch r.URL.Host {
	case "api.ipquery.io":
		return 200, independentQuery
	case "ipwho.is":
		return 200, independentGeo
	case "internetdb.shodan.io":
		return 200, independentDB

	default:
		return 503, `{}`
	}
}
func intelligenceFact(report NetworkDiagnosticReport, label string) NetworkDiagnosticFact {
	for _, f := range report.Facts {
		if f.Group == "risk" && f.Label == label {
			return f
		}
	}
	return NetworkDiagnosticFact{}
}
func fixtureIntelligence(override func(*http.Request) (int, string)) *intelligenceFixture {
	if override == nil {
		override = intelligenceDefault
	}
	return &intelligenceFixture{calls: map[string]int{}, response: override}
}
func TestIndependentSourcesPreserveNotIndexedAndIPv6Status(t *testing.T) {
	for _, ip := range []string{independentIP, "2001:4860:4860::8888"} {
		t.Run(ip, func(t *testing.T) {
			client := fixtureIntelligence(func(r *http.Request) (int, string) {
				if r.URL.Host == "internetdb.shodan.io" {
					return 404, `{"detail":"No information available"}`
				}
				status, body := intelligenceDefault(r)
				return status, strings.ReplaceAll(body, independentIP, ip)
			})
			report := NewExitLookup().DiagnoseIP(context.Background(), client, ip, "route")
			if client.calls["ipx.al"] != 0 || intelligenceFact(report, "开放端口").State != "unknown" {
				t.Fatal(report, client.calls)
			}
			expected := "not_indexed"
			if strings.Contains(ip, ":") {
				expected = "unsupported"
			}
			if report.Providers[2].State != expected {
				t.Fatal(report.Providers)
			}
		})
	}
}
func TestIndependentSourcesWrongIdentityRetainsDirectData(t *testing.T) {
	client := fixtureIntelligence(func(r *http.Request) (int, string) {
		if r.URL.Host == "api.ipquery.io" {
			return 200, strings.ReplaceAll(independentQuery, independentIP, "8.8.8.8")
		}
		return intelligenceDefault(r)
	})
	report := NewExitLookup().DiagnoseIP(context.Background(), client, independentIP, "route")
	if report.Country != "US" || len(report.Sources) != 1 || report.ProviderError != "" || intelligenceFact(report, "代理").State != "unknown" {
		t.Fatal(report)
	}
	if report.Providers[0].ErrorCode != "provider_ip_mismatch" || client.calls["ipx.al"] != 0 {
		t.Fatal(report.Providers)
	}
}
func TestIndependentSourcesShareExitQuotaAndCooldown(t *testing.T) {
	lookup := NewExitLookup()
	client := fixtureIntelligence(func(r *http.Request) (int, string) {
		if r.URL.Host == "ipwho.is" {
			return 429, `{}`
		}
		return intelligenceDefault(r)
	})
	lookup.fetchIntelligence(context.Background(), client, "ipwho.is", independentIP, "route")
	next := lookup.fetchIntelligence(context.Background(), client, "ipwho.is", independentIP, "route")
	if client.calls["ipwho.is"] != 1 || next.attempt.ErrorCode == "" {
		t.Fatal(client.calls, next)
	}
	// An exact-IP lookup must not infer an anonymous request source from the target.
	if lookup.sources["ipwho.is/route"] != "" {
		t.Fatal("queried target was learned as caller")
	}
	for _, p := range lookup.providers {
		if p.id == "ipwho.is" && lookup.quota(p, "route").calls != 1 {
			t.Fatal("quota was not shared")
		}
	}
	if lookup.Snapshot().InFlight != 0 {
		t.Fatal("leaked request lease")
	}
}
func TestIndependentSourcesCancellationJoinsWithoutFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	calls, active := 0, 0
	started := make(chan struct{}, 3)
	client := diagnosticTestClient(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		calls++
		active++
		mu.Unlock()
		started <- struct{}{}
		<-r.Context().Done()
		mu.Lock()
		active--
		mu.Unlock()
		return nil, r.Context().Err()
	})
	done := make(chan NetworkDiagnosticReport, 1)
	go func() { done <- NewExitLookup().DiagnoseIP(ctx, client, independentIP, "route") }()
	for range 3 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("probe did not start")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("did not join cancelled workers")
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 3 || active != 0 {
		t.Fatal(calls, active)
	}
}
func TestIndependentSourcesRecordsPortsWithoutInferringRiskFromTags(t *testing.T) {
	client := fixtureIntelligence(func(r *http.Request) (int, string) {
		if r.URL.Host == "internetdb.shodan.io" {
			return 200, `{"ip":"203.0.113.24","ports":[443],"vulns":["CVE-2026-0001"],"tags":["vpn"]}`
		}
		return intelligenceDefault(r)
	})
	report := NewExitLookup().DiagnoseIP(context.Background(), client, independentIP, "route")
	found := false
	for _, fact := range report.Facts {
		if fact.Group == "exposure_detail" && fact.Label == "开放端口" && fact.Value == "443" {
			found = true
		}
	}
	if !found || client.calls["ipx.al"] != 0 || intelligenceFact(report, "Shodan 标签").State != "information" {
		t.Fatal(report, client.calls)
	}
}

func TestIndependentSourcesExposureRejectsInvalidBoundaryValues(t *testing.T) {
	for _, body := range []string{
		`{"ip":"203.0.113.24","ports":[65536],"vulns":[],"tags":[]}`,
		`{"ip":"203.0.113.24","ports":[],"vulns":"not-an-array","tags":[]}`,
		`{"ip":"203.0.113.24","ports":[],"vulns":[],"tags":["untrusted\ntext"]}`,
	} {
		report := parseInternetDB([]byte(body), netip.MustParseAddr(independentIP))
		if report.ProviderError != "provider_response_invalid" || len(report.Facts) != 0 {
			t.Fatal("malformed external evidence accepted", report)
		}
	}
}

func TestIndependentSourcesKeepDirectEvidenceAndLeaveUncoveredUnknown(t *testing.T) {
	client := fixtureIntelligence(nil)
	report := NewExitLookup().DiagnoseIP(context.Background(), client, independentIP, "environment")
	if len(report.Providers) != 3 || len(report.Sources) != 2 || len(client.calls) != 3 ||
		report.ProviderError != "" || report.Score != nil {
		t.Fatal(report.Providers, report.Sources, client.calls)
	}
	if report.Sources[0].City == report.Sources[1].City {
		t.Fatal("direct disagreement was lost", report.Sources)
	}
	if got := intelligenceFact(report, "VPN"); got.ProviderID != "ipquery" || got.State != "clear" {
		t.Fatal(got)
	}
	if got := intelligenceFact(report, "邮件 DNSBL"); got != (NetworkDiagnosticFact{}) {
		t.Fatal(got)
	}
}
