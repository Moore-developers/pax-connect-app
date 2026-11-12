package environment

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"pax-connect/daemon/internal/measurement"
	"strings"
	"testing"
	"time"
)

func TestEnvironmentDNSCountsDistinctObservedResolvers(t *testing.T) {
	rows := `[{"ip":"8.8.8.8","country":"us","org":"Google","type":"dns"},{"ip":"8.8.8.8","country":"us","type":"dns"},{"ip":"1.1.1.1","country":"au","org":"Cloudflare","type":"dns"},{"ip":"9.9.9.9","country":"us","type":"ip"},{"ip":"192.168.1.1","country":"us","type":"dns"}]`
	values, err := parseResolvers([]byte(rows))
	if err != nil || len(values) != 2 || values[0].Country != "US" {
		t.Fatalf("unexpected resolver evidence: %#v, %v", values, err)
	}
	if _, err := parseResolvers([]byte(`[{"type":"error","text":"No DNS servers found"}]`)); err == nil {
		t.Fatal("empty result must not be success")
	}
}
func TestEnvironmentBrowserRejectsUnboundedAndInvalidSamples(t *testing.T) {
	for _, value := range []Sample{{Timezone: "invented/timezone", RTCState: "empty"}, {RTCAddresses: []string{"https://attacker.example/"}, RTCState: "complete"}, {Browser: strings.Repeat("x", 81), RTCState: "empty"}, {RTCState: "safe"}} {
		if validateBrowser(value) == nil {
			t.Fatalf("accepted invalid browser sample: %#v", value)
		}
	}
	if err := validateBrowser(Sample{Timezone: "America/Los_Angeles", Languages: []string{"zh-CN"}, RTCAddresses: []string{"8.8.8.8", "host-1.local"}, RTCState: "complete"}); err != nil {
		t.Fatal(err)
	}
}
func TestEnvironmentLoopbackRejectsForeignHostAndOrigin(t *testing.T) {
	s := &Session{state: Snapshot{ID: "nonce"}, origin: "http://127.0.0.1:1234", path: "/secret/"}
	for _, item := range []struct {
		path, host, origin string
		status             int
	}{{"/secret/", "attacker.example", "", 404}, {"/wrong/", "127.0.0.1:1234", "", 404}, {"/secret/result", "127.0.0.1:1234", "https://attacker.example", 403}} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:1234"+item.path, nil)
		r.Host = item.host
		r.Header.Set("Origin", item.origin)
		w := httptest.NewRecorder()
		s.serve(context.Background(), w, r)
		if w.Code != item.status {
			t.Fatalf("got %d want %d", w.Code, item.status)
		}
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1:1234/secret/", nil)
	w := httptest.NewRecorder()
	s.serve(context.Background(), w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `nonce="nonce"`) || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("page security headers or nonce missing")
	}
}

type fixtureClient func(*http.Request) (*http.Response, error)

func (f fixtureClient) Do(r *http.Request) (*http.Response, error) { return f(r) }
func TestEnvironmentCurrentProbeUsesFixedIPFamilyAndNoRoute(t *testing.T) {
	client := fixtureClient(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "ipv4.icanhazip.com" {
			t.Fatalf("unexpected destination %s", r.URL.Host)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("8.8.8.8\n"))}, nil
	})
	if value, check := ipCheck(context.Background(), client, false); value != "8.8.8.8" || check.State != "observed" {
		t.Fatal(value)
	}
	if publicIP("127.0.0.1") != "" || publicIP("10.1.1.1") != "" {
		t.Fatal("private address accepted as public egress")
	}
}
func TestEnvironmentBrowserRejectsTrailingJSONAndOversizedBody(t *testing.T) {
	s := &Session{state: Snapshot{ID: "nonce"}, origin: "http://127.0.0.1:1234", path: "/secret/"}
	for _, body := range []string{`{"rtcState":"empty"} {}`, `{"rtcState":"empty","browser":"` + strings.Repeat("x", 33000) + `"}`, `{"rtcState":"empty","unexpected":true}`} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:1234/secret/result", strings.NewReader(body))
		r.Header.Set("Origin", s.origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.serve(context.Background(), w, r)
		if w.Code != 400 {
			t.Fatalf("invalid body accepted: %d", w.Code)
		}
	}
}

func TestEnvironmentCheckRetainsHTTPFailureAndRejectsForgedTargets(t *testing.T) {
	client := fixtureClient(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader("private body"))}, nil
	})
	ip, check := ipCheck(context.Background(), client, false)
	if ip != "" || check.ErrorCode != "probe_http_429" || check.Value != "" {
		t.Fatal(check)
	}
	valid := Check{Kind: "rtc_cf", Target: "stun.cloudflare.com:3478", State: "timeout", ErrorCode: "rtc_server_701", At: time.Now()}
	if err := validateChecks([]Check{valid}); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []Check{
		{Kind: "rtc_cf", Target: "attacker.example", State: "observed", At: time.Now()},
		{Kind: "intelligence", Target: "ipx.al", State: "observed", At: time.Now()},
		{Kind: "rtc_cf", Target: "stun.cloudflare.com:3478", State: "safe", At: time.Now()},
		{Kind: "rtc_cf", Target: "stun.cloudflare.com:3478", State: "empty", ErrorCode: "secret", At: time.Now()},
	} {
		if validateChecks([]Check{invalid}) == nil {
			t.Fatal("accepted untrusted check", invalid)
		}
	}
	if validateChecks([]Check{valid, valid}) == nil {
		t.Fatal("duplicate check accepted")
	}
}

func TestEnvironmentRetrySameIPKeepsSuccessOnFailure(t *testing.T) {
	score := 55
	client := fixtureClient(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("private body"))}, nil
	})
	sample := Sample{IPv4: "8.8.8.8", Intelligence: measurement.NetworkDiagnosticReport{Score: &score}}
	s := &Session{client: client, lookup: measurement.NewExitLookup(), state: Snapshot{Mac: sample, Browser: sample, Complete: true}}
	value := s.RetrySources(context.Background())
	if value.Mac.Intelligence.Score == nil || *value.Mac.Intelligence.Score != 55 || value.Browser.Intelligence.Score == nil {
		t.Fatal("failed retry erased same-IP evidence")
	}
	for _, sample := range []Sample{value.Mac, value.Browser} {
		if len(sample.Checks) != 1 || sample.Checks[0].ErrorCode != "intelligence_unavailable" {
			t.Fatal(sample.Checks)
		}
	}
}

func TestEnvironmentBrowserCannotSetCompletionOrRawIPError(t *testing.T) {
	s := &Session{lookup: measurement.NewExitLookup(), state: Snapshot{ID: "nonce", Mac: Sample{Complete: true}}, origin: "http://127.0.0.1:1234", path: "/secret/", cancel: func() {}}
	s.client = fixtureClient(func(*http.Request) (*http.Response, error) {
		sample := s.Read().Browser
		if sample.Complete || sample.IPError != "" {
			t.Fatal("browser controlled daemon-owned fields", sample.Complete, sample.IPError)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ip":"8.8.8.8"}`))}, nil
	})
	r := httptest.NewRequest("POST", "http://127.0.0.1:1234/secret/result", strings.NewReader(`{"ipv4":"8.8.8.8","complete":true,"ipError":"untrusted text","rtcState":"empty"}`))
	r.Header.Set("Origin", s.origin)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.serve(context.Background(), w, r)
	if w.Code != 204 || !s.Read().Browser.Complete {
		t.Fatal(w.Code)
	}
}
