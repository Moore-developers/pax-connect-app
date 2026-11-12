package measurement

import (
	"context"
	"io"
	"net/http"
	"net/netip"
	"slices"
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
	case "ipx.al":
		return 200, `{"ip":"203.0.113.24","location":{"country":"CA"},"health":{"score":77},"native":{"verdict":"native","regCountry":"US","useCountry":"US"},"risk":{"ipquery":{"risk":99},"abuseipdb":{"score":0,"reports":0}},"dnsbl":{"list":[{"name":"TestList","status":"clean"}]},"sources":[{"name":"forwarded","country":"CA","asn":64501}],"categories":[{"label":"匿名","checks":[{"label":"VPN","hit":true},{"label":"代理","hit":true},{"label":"iCloud 中继","hit":false},{"label":"滥用标记","hit":false},{"label":"邮件 DNSBL","hit":false},{"label":"自签证书","hit":true}]}]}`
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
func TestIndependentSourcesSupplementUncoveredChecksPreservingFalseZeroAndConflict(t *testing.T) {
	client := fixtureIntelligence(nil)
	report := NewExitLookup().DiagnoseIP(context.Background(), client, independentIP, "route/epoch")
	if report.ProviderError != "" || len(report.Sources) != 3 || len(report.Providers) != 4 || client.calls["ipx.al"] != 1 || report.Score == nil {
		t.Fatal(report, client.calls)
	}
	if report.Sources[2].Name != "forwarded" || report.Sources[2].ProviderID != "ipx.al" || report.Country != "US" {
		t.Fatal("full supplemental comparison or independent location lost", report)
	}
	if report.Sources[0].City == report.Sources[1].City {
		t.Fatal("fixture must retain disagreement")
	}
	for _, label := range []string{"代理", "VPN", "Tor", "机房 IP", "移动网络", "开放端口", "已知漏洞", "Shodan 标签"} {
		if f := intelligenceFact(report, label); f.State != "clear" {
			t.Fatal(label, f)
		}
	}
	for _, label := range []string{"iCloud 中继", "滥用标记", "邮件 DNSBL"} {
		if f := intelligenceFact(report, label); f.State != "clear" || f.ProviderID != "ipx.al" {
			t.Fatal("supplement omitted", f)
		}
	}
	if intelligenceFact(report, "自签证书").State != "hit" || intelligenceFact(report, "伪家宽").State != "unknown" {
		t.Fatal("unknown or hit lost", report)
	}
	for _, f := range report.Facts {
		if f.Label == "IPQuery 风险" && (f.Value != "0" || f.ProviderID != "ipquery") {
			t.Fatal("supplement replaced direct zero", f)
		}
		if f.Label == "AbuseIPDB 置信度" && f.ProviderID != "ipx.al" {
			t.Fatal("forwarded source was mislabeled", f)
		}
	}
	if report.IPType != "" {
		t.Fatal("negative hosting/mobile flags invented residential type")
	}
}
func TestIndependentSourcesFallbackOnlyMissingCapability(t *testing.T) {
	client := fixtureIntelligence(func(r *http.Request) (int, string) {
		if r.URL.Host == "api.ipquery.io" {
			return 200, strings.Replace(independentQuery, `"is_vpn":false,`, "", 1)
		}
		return intelligenceDefault(r)
	})
	report := NewExitLookup().DiagnoseIP(context.Background(), client, independentIP, "route")
	if client.calls["ipx.al"] != 1 || len(report.Sources) != 3 || report.Country != "US" {
		t.Fatal(report, client.calls)
	}
	if f := intelligenceFact(report, "VPN"); f.State != "hit" || f.ProviderID != "ipx.al" {
		t.Fatal(f)
	}
	if f := intelligenceFact(report, "代理"); f.State != "clear" || f.ProviderID != "ipquery" {
		t.Fatal("fallback overwrote direct evidence", f)
	}
	fallback := report.Providers[len(report.Providers)-1]
	if !slices.Contains(fallback.FallbackFor, "risk.vpn") || slices.Contains(fallback.FallbackFor, "risk.proxy") || fallback.FallbackReason != "independent_coverage_missing" {
		t.Fatal(fallback)
	}
}
func TestIndependentSourcesSupplementPreservesNotIndexedAndIPv6Status(t *testing.T) {
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
			if client.calls["ipx.al"] != 1 || intelligenceFact(report, "开放端口").State != "unknown" {
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
func TestIndependentSourcesWrongIdentityAndFailedFallbackRetainDirectData(t *testing.T) {
	client := fixtureIntelligence(func(r *http.Request) (int, string) {
		if r.URL.Host == "api.ipquery.io" {
			return 200, strings.ReplaceAll(independentQuery, independentIP, "8.8.8.8")
		}
		if r.URL.Host == "ipx.al" {
			return 429, `{}`
		}
		return intelligenceDefault(r)
	})
	report := NewExitLookup().DiagnoseIP(context.Background(), client, independentIP, "route")
	if report.Country != "US" || len(report.Sources) != 1 || report.ProviderError != "" || intelligenceFact(report, "代理").State != "unknown" {
		t.Fatal(report)
	}
	if report.Providers[0].ErrorCode != "provider_ip_mismatch" || report.Providers[len(report.Providers)-1].HTTPStatus != 429 {
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
	if !found || client.calls["ipx.al"] != 1 || intelligenceFact(report, "Shodan 标签").State != "information" {
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

func TestIndependentSourcesSupplementFailureKeepsEveryDirectResult(t *testing.T) {
	client := fixtureIntelligence(func(r *http.Request) (int, string) {
		if r.URL.Host == "ipx.al" {
			return 503, `{}`
		}
		return intelligenceDefault(r)
	})
	report := NewExitLookup().DiagnoseIP(context.Background(), client, independentIP, "route")
	if report.ProviderError != "" || len(report.Sources) != 2 || len(report.Providers) != 4 || client.calls["ipx.al"] != 1 {
		t.Fatal(report, client.calls)
	}
	for _, label := range []string{"代理", "VPN", "Tor", "开放端口", "已知漏洞"} {
		if intelligenceFact(report, label).State != "clear" {
			t.Fatal("supplement failure hid direct evidence", label)
		}
	}
	if intelligenceFact(report, "邮件 DNSBL").State != "unknown" || report.Providers[3].State != "failed" {
		t.Fatal(report)
	}
}
func TestNetworkSupplementExposureDetailsAndBoundaries(t *testing.T) {
	report := parseNetworkReport([]byte(`{"ip":"203.0.113.24","exposure":{"ports":[22,443],"tags":["vpn"],"vulns":2},"network":{"humanPct":24.5,"radarDate":"2026-11-08"}}`), netip.MustParseAddr(independentIP))
	if report.ProviderError != "" {
		t.Fatal(report)
	}
	for _, expected := range []struct{ label, value string }{{"开放端口", "22, 443"}, {"已知漏洞", "2 项"}, {"ASN 人类流量比例", "24.5%"}} {
		found := false
		for _, fact := range report.Facts {
			if fact.Label == expected.label && strings.Contains(fact.Value, expected.value) && fact.ProviderID == "ipx.al" {
				found = true
			}
		}
		if !found {
			t.Fatal("supplement detail lost", expected, report)
		}
	}
	for _, ports := range []string{`[65536]`, `[-1]`} {
		invalid := parseNetworkReport([]byte(`{"ip":"203.0.113.24","exposure":{"ports":`+ports+`}}`), netip.MustParseAddr(independentIP))
		if invalid.ProviderError != "provider_response_invalid" {
			t.Fatal("invalid supplemental port accepted", invalid)
		}
	}
}

func TestIndependentSourcesSupplementAdmitsRouteAndEnvironmentWithoutSerialBudget(t *testing.T) {
	lookup := NewExitLookup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 2)
	client := diagnosticTestClient(func(r *http.Request) (*http.Response, error) {
		started <- struct{}{}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	var workers sync.WaitGroup
	for _, scope := range []string{"route/epoch", "environment/session"} {
		workers.Add(1)
		go func(scope string) {
			defer workers.Done()
			lookup.fetchIntelligence(ctx, client, "ipx.al", independentIP, scope)
		}(scope)
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			cancel()
			workers.Wait()
			t.Fatal("route and environment serialized within the shared 6s budget")
		}
	}
	if lookup.Snapshot().InFlight != 2 {
		t.Fatal("missing active requests")
	}
	cancel()
	workers.Wait()
	if lookup.Snapshot().InFlight != 0 {
		t.Fatal("cancelled supplement leaked admission")
	}
}
