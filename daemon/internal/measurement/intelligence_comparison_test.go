package measurement

import (
	"context"
	"net/http"
	"net/netip"
	"slices"
	"testing"
)

func TestRouteComparisonUsesSixExactIPRequests(t *testing.T) {
	const ip = independentIP
	samples := map[string]string{
		"https://api.db-ip.com/v2/free/" + ip:                              `{"ipAddress":"203.0.113.24","countryCode":"US","city":"Example"}`,
		"https://api.ip2location.io/?ip=" + ip:                             `{"ip":"203.0.113.24","country_code":"US","city_name":"Example","asn":"64500","as":"Network","is_proxy":false}`,
		"https://api.ipapi.is/?q=" + ip:                                    `{"ip":"203.0.113.24","country":"United States","city":"Example","asn":"AS64500 Example LLC","company":"Example LLC"}`,
		"https://proxycheck.io/v3/" + ip:                                   `{"status":"ok","203.0.113.24":{"network":{"asn":"AS64500","provider":"Example"},"location":{"country_code":"US","city_name":"Example"},"detections":{"vpn":true,"proxy":false}}}`,
		"https://www.iplocate.io/api/lookup/" + ip:                         `{"ip":"203.0.113.24","country_code":"US","city":"Example","asn":{"asn":"AS64500"},"privacy":{"is_tor":false}}`,
		"https://stat.ripe.net/data/network-info/data.json?resource=" + ip: `{"status":"ok","data":{"prefix":"203.0.113.0/24","asns":["64500","64501"]}}`,
	}
	client := fixtureIntelligence(func(req *http.Request) (int, string) {
		if body, ok := samples[req.URL.String()]; ok {
			if req.Method != http.MethodGet || req.Header.Get("Accept") != "application/json" {
				t.Errorf("unexpected lookup request for %s", req.URL.Host)
			}
			return 200, body
		}
		return intelligenceDefault(req)
	})
	q := NewExitLookup()
	base := q.DiagnoseIP(context.Background(), client, ip, "environment/one")
	if len(base.Providers) != 3 {
		t.Fatal("current environment unexpectedly queried new providers", base.Providers)
	}
	report := q.DiagnoseRouteIP(context.Background(), client, ip, "route/epoch")
	if len(report.Providers) != 9 || len(report.Sources) != 8 || report.ProviderError != "" {
		t.Fatal(report.Providers, report.Sources)
	}
	for _, p := range report.Providers[3:] {
		if p.State != "observed" || p.QueryIP != ip || p.HTTPStatus != 200 {
			t.Fatal(p)
		}
	}
	i := slices.IndexFunc(report.Sources, func(s NetworkDiagnosticSource) bool { return s.ProviderID == "ipapi.is" })
	if i < 0 || report.Sources[i].ASN != "AS64500" || report.Sources[i].Country != "United States" {
		t.Fatal(report.Sources)
	}
	if got := intelligenceFact(report, "VPN"); got.ProviderID != "ipquery" || got.State != "clear" {
		t.Fatal("comparison provider replaced existing risk policy", got)
	}
	for _, want := range []NetworkDiagnosticFact{
		{Group: "source_risk", ProviderID: "proxycheck.io", Label: "VPN", State: "hit"},
		{Group: "source_risk", ProviderID: "ip2location.io", Label: "代理", State: "clear"},
		{Group: "source_network", ProviderID: "ripestat", Label: "宣告前缀", Value: "203.0.113.0/24", State: "information"},
	} {
		if !slices.ContainsFunc(report.Facts, func(f NetworkDiagnosticFact) bool {
			return f.Group == want.Group && f.ProviderID == want.ProviderID && f.Label == want.Label &&
				f.State == want.State && (want.Value == "" || f.Value == want.Value)
		}) {
			t.Fatal("missing comparison evidence", want)
		}
	}
}

func TestRouteComparisonChecksIdentityAndQuota(t *testing.T) {
	ip := netip.MustParseAddr("2001:db8::1")
	for _, tc := range []struct{ id, body, code string }{
		{"db-ip", `{"ipAddress":"2001:db8::2","countryCode":"US"}`, "provider_ip_mismatch"},
		{"iplocate", `{"ip":"2001:0db8:0000:0000:0000:0000:0000:0001","country_code":"US"}`, ""},
		{"ripestat", `{"status":"ok","data":{"prefix":"2001:db9::/32","asns":["64500"]}}`, "provider_ip_mismatch"},
	} {
		if got := parseRouteComparison(tc.id, []byte(tc.body), ip); got.ProviderError != tc.code {
			t.Error(tc.id, got.ProviderError)
		}
	}
	partial := NewExitLookup().fetchIntelligence(context.Background(), fixtureIntelligence(func(*http.Request) (int, string) {
		return 200, `{"ipAddress":"203.0.113.24","countryCode":"US"}`
	}), "db-ip", independentIP, "route/epoch")
	if partial.attempt.State != "partial" || partial.attempt.ErrorCode != "provider_fields_missing" ||
		len(partial.report.Sources) != 1 {
		t.Fatal(partial)
	}
	client := fixtureIntelligence(func(*http.Request) (int, string) {
		return 401, `{"error":{"error_code":10000,"error_message":"Limit exceeded"}}`
	})
	q := NewExitLookup()
	first := q.fetchIntelligence(context.Background(), client, "ip2location.io", independentIP, "route/epoch")
	second := q.fetchIntelligence(context.Background(), client, "ip2location.io", independentIP, "route/epoch")
	if first.attempt.HTTPStatus != 401 || first.attempt.ErrorCode != "exit_rate_limited" ||
		second.attempt.ErrorCode == "" || client.calls["api.ip2location.io"] != 1 {
		t.Fatal(first.attempt, second.attempt, client.calls)
	}
}
