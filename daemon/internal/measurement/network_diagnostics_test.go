package measurement

import (
	"context"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type diagnosticTestClient func(*http.Request) (*http.Response, error)

func (f diagnosticTestClient) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestNetworkReportIdentityCoverageAndUnknown(t *testing.T) {
	body := []byte(`{"ip":"203.0.113.24","version":3,"health":{"score":55,"ceiling":55,"model":"v3.6","coverage":{"intel":2,"intelTotal":5}},"location":{"country":"US","timezone":"America/Chicago"},"sources":[{"name":"one","country":"US","asn":64500},{"name":"two","country":"CA","city":"Toronto","asn":64500}],"type":{"verdict":"hosting","votes":[{"src":"one","v":"hosting"}]},"dnsbl":{"list":[{"name":"a","status":"clean"},{"name":"b","status":"unknown"}]},"categories":[{"label":"匿名","checks":[{"label":"VPN","hit":null},{"label":"代理","hit":false}]}]}`)
	result := parseNetworkReport(body, netip.MustParseAddr("203.0.113.24"))
	if result.ProviderError != "" || result.Score == nil || *result.Score != 55 || len(result.Sources) != 2 || result.Sources[1].Country != "CA" || result.IntelAnswered != 2 {
		t.Fatalf("lost conflicting or partial evidence: %+v", result)
	}
	states := map[string]string{}
	for _, f := range result.Facts {
		states[f.Label] = f.State
	}
	if states["VPN"] != "unknown" || states["代理"] != "clear" || states["b"] != "unknown" {
		t.Fatal(states)
	}
	mismatch := parseNetworkReport(body, netip.MustParseAddr("203.0.113.25"))
	if mismatch.ProviderError != "provider_ip_mismatch" || mismatch.Score != nil || len(mismatch.Sources) != 0 {
		t.Fatal("wrong IP must not publish evidence", mismatch)
	}
}
func TestNetworkReportRejectsInvalidScoreAndLeavesMissingUnknown(t *testing.T) {
	for _, body := range []string{`{"ip":"203.0.113.24","health":{"score":101}}`, `{"ip":"203.0.113.24","health":{"coverage":{"intel":8,"intelTotal":2}}}`} {
		if v := parseNetworkReport([]byte(body), netip.MustParseAddr("203.0.113.24")); v.ProviderError != "provider_response_invalid" {
			t.Fatal(v)
		}
	}
	v := parseNetworkReport([]byte(`{"ip":"203.0.113.24","location":{"timezone":"invented/zone"}}`), netip.MustParseAddr("203.0.113.24"))
	if v.Score != nil || v.Timezone != "" {
		t.Fatal("missing score/timezone cannot become valid", v)
	}
}
func TestNetworkProbeResponsesAndCancellation(t *testing.T) {
	client := diagnosticTestClient(func(r *http.Request) (*http.Response, error) {
		code := 200
		if strings.Contains(r.URL.Host, "api.") {
			code = 401
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	r := DiagnoseNetwork(context.Background(), client, "services")
	if len(r.Probes) != 12 {
		t.Fatal(r)
	}
	for _, p := range r.Probes {
		if p.HTTPStatus == 401 && p.ExpectedResponse {
			t.Fatal("authentication response is not service permission", p)
		}
	}
	var calls atomic.Int32
	stopped := diagnosticTestClient(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	v := DiagnoseNetwork(ctx, stopped, "quick")
	if len(v.Probes) != 12 || calls.Load() != 0 {
		t.Fatal("cancelled work must not become failures", v)
	}
}

func TestNetworkProbeProgressPrecedesFinalResult(t *testing.T) {
	updates := make(chan DiagnosticProgress, 64)
	ctx, cancel := context.WithCancel(WithDiagnosticObserver(context.Background(), func(p DiagnosticProgress) { updates <- p }))
	defer cancel()
	permit := make(chan struct{}, 1)
	client := diagnosticTestClient(func(r *http.Request) (*http.Response, error) {
		select {
		case <-permit:
			return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(""))}, nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})
	done := make(chan NetworkDiagnosticReport, 1)
	go func() { done <- DiagnoseNetwork(ctx, client, "services") }()
	permit <- struct{}{}
	for {
		select {
		case update := <-updates:
			if update.ProbesTotal != 12 {
				t.Fatal(update)
			}
			if update.ProbesCompleted == 1 {
				goto observed
			}
		case <-time.After(time.Second):
			t.Fatal("no progress before final result")
		}
	}
observed:
	select {
	case <-done:
		t.Fatal("published only after final result")
	default:
	}
	cancel()
	var result NetworkDiagnosticReport
	select {
	case result = <-done:
	case <-time.After(time.Second):
		t.Fatal("cancel did not join")
	}
	if len(result.Probes) != 12 {
		t.Fatal(len(result.Probes))
	}
	maxCompleted := 0
	for len(updates) > 0 {
		maxCompleted = max(maxCompleted, (<-updates).ProbesCompleted)
	}
	if maxCompleted != 12 {
		t.Fatal("settled count excludes failed/cancelled requests", maxCompleted)
	}
}
func TestNetworkProviderBodyAndRouteBoundEndpoint(t *testing.T) {
	var address string
	c := diagnosticTestClient(func(r *http.Request) (*http.Response, error) {
		address = r.URL.String()
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", networkReportLimit+1)))}, nil
	})
	v := NewExitLookup().fetchIntelligence(context.Background(), c, "ipx.al", "203.0.113.24", "test").report
	if address != "https://ipx.al/api/report/203.0.113.24" || v.ProviderError != "provider_response_invalid" {
		t.Fatal(address, v)
	}
}

func TestNetworkRawRiskKeepsZeroSeparateFromMissingAndInvalid(t *testing.T) {
	body := []byte(`{"ip":"203.0.113.24","health":{"score":55},"risk":{"proxycheck":null,"ipquery":{"risk":0},"abuseipdb":{"score":-1,"reports":4}}}`)
	v := parseNetworkReport(body, netip.MustParseAddr("203.0.113.24"))
	if v.ProviderError != "" {
		t.Fatal(v)
	}
	facts := map[string]NetworkDiagnosticFact{}
	for _, f := range v.Facts {
		facts[f.Label] = f
	}
	if facts["IPQuery 风险"].Value != "0" || facts["IPQuery 风险"].State != "information" {
		t.Fatal("zero was lost", facts)
	}
	if facts["proxycheck 风险"].State != "unknown" || facts["AbuseIPDB 置信度"].State != "unknown" || facts["AbuseIPDB 举报数"].Value != "4" {
		t.Fatal("missing/invalid conflated with zero", facts)
	}
}

func TestNetworkFullCoversServicesAndBudgetEvidence(t *testing.T) {
	client := diagnosticTestClient(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://user:password@login.example/path?secret=1"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	full := DiagnoseNetwork(context.Background(), client, "full")
	hosts := map[string]bool{}
	for _, p := range full.Probes {
		hosts[p.Target] = true
		if p.State != "responded" || p.RedirectHost != "login.example" {
			t.Fatalf("missing response evidence: %+v", p)
		}
	}
	if len(full.Probes) != 22 || len(hosts) != 14 {
		t.Fatalf("incomplete full capture: %d probes/%d targets", len(full.Probes), len(hosts))
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	result := DiagnoseNetwork(ctx, client, "services")
	if len(result.Probes) != 12 {
		t.Fatal("missing queued targets", len(result.Probes))
	}
	for _, p := range result.Probes {
		if p.State != "not_started" || p.ErrorCode != "diagnostic_budget_exhausted" {
			t.Fatalf("misreported budget as network failure: %+v", p)
		}
	}
}
