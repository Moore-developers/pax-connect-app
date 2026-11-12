package measurement

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func geoResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func singleProvider(t *testing.T, id string) *ExitLookup {
	t.Helper()
	q := NewExitLookup()
	for _, p := range q.providers {
		if p.id == id {
			q.providers = []exitProvider{p}
			return q
		}
	}
	t.Fatal("fixture provider missing")
	return nil
}
func TestExitLookupNormalizesProviderSchemas(t *testing.T) {
	cases := []struct{ id, body, ip, asn string }{
		{"ipwho.is", `{"ip":"8.8.8.8","country":"United States","country_code":"US","region":"California","city":"Mountain View","connection":{"isp":"Google","asn":15169}}`, "8.8.8.8", "AS15169"},
		{"ip.sb", `{"ip":"8.8.4.4","country":"United States","region":"California","city":"Mountain View","asn":15169}`, "8.8.4.4", "AS15169"},
		{"freeipapi", `{"ipAddress":"8.8.8.8","countryCode":"US","countryName":"United States","regionName":"California","cityName":"Mountain View","asn":15169,"asnOrganization":"Google"}`, "8.8.8.8", "AS15169"},
		{"geojs", `{"ip":"8.8.8.8","country":"United States","region":"California","city":"Mountain View","asn":15169,"organization_name":"Google"}`, "8.8.8.8", "AS15169"},
		{"ipquery", `{"ip":"8.8.8.8","location":{"country":"United States","country_code":"US","state":"California","city":"Mountain View"},"isp":{"asn":"AS15169","org":"Google","isp":"Google"}}`, "8.8.8.8", "AS15169"},
		{"ipapi.is", `{"ip":"8.8.8.8","location":{"country":"United States","country_code":"US","state":"California","city":"Mountain View"},"company":{"name":"Google"},"asn":{"asn":15169}}`, "8.8.8.8", "AS15169"},
		{"cloudflare", "ip=8.8.8.8\nloc=US\ncolo=LAX\n", "8.8.8.8", ""},
	}
	for _, fixture := range cases {
		t.Run(fixture.id, func(t *testing.T) {
			q := singleProvider(t, fixture.id)
			client := fixtureClient(func(*http.Request) (*http.Response, error) { return geoResponse(fixture.body), nil })
			value, code := q.Query(context.Background(), client, ExitQuery{Scope: "route"})
			if code != "" || value.IP != fixture.ip || value.ASN != fixture.asn || value.Source != fixture.id {
				t.Fatal(value, code)
			}
			if fixture.id == "cloudflare" {
				if value.City != "" || value.CountryCode != "US" {
					t.Fatal("colo became exit city")
				}
			} else if value.Region != "California" || value.City != "Mountain View" {
				t.Fatal("location lost", value)
			}
		})
	}
}
func TestExitLookupProviderFailuresCooldownAndRetryAfter(t *testing.T) {
	q := singleProvider(t, "ipwho.is")
	now := time.Now()
	q.now = func() time.Time { return now }
	calls := 0
	client := fixtureClient(func(*http.Request) (*http.Response, error) {
		calls++
		r := geoResponse("")
		r.StatusCode = 503
		return r, nil
	})
	for i := 0; i < 3; i++ {
		_, _ = q.Query(context.Background(), client, ExitQuery{Scope: "r"})
	}
	before := calls
	_, code := q.Query(context.Background(), client, ExitQuery{Scope: "other"})
	if calls != before || code != "exit_services_unavailable" {
		t.Fatal("cooldown contacted service", code, calls)
	}
	now = now.Add(61 * time.Second)
	_, _ = q.Query(context.Background(), client, ExitQuery{Scope: "r"})
	if calls == before {
		t.Fatal("cooldown never released")
	}
}
func TestExitLookupRateLimitImmediatelyBacksOff(t *testing.T) {
	q := singleProvider(t, "ipwho.is")
	now := time.Now()
	q.now = func() time.Time { return now }
	calls := 0
	client := fixtureClient(func(*http.Request) (*http.Response, error) {
		calls++
		r := geoResponse("")
		r.StatusCode = 429
		r.Header.Set("Retry-After", "120")
		return r, nil
	})
	_, _ = q.Query(context.Background(), client, ExitQuery{Scope: "r"})
	before := calls
	_, _ = q.Query(context.Background(), client, ExitQuery{Scope: "other"})
	if calls != before {
		t.Fatal("429 ignored")
	}
	now = now.Add(61 * time.Second)
	_, _ = q.Query(context.Background(), client, ExitQuery{Scope: "r"})
	if calls != before {
		t.Fatal("RetryAfter shortened")
	}
	now = now.Add(61 * time.Second)
	_, _ = q.Query(context.Background(), client, ExitQuery{Scope: "r"})
	if calls == before {
		t.Fatal("never recovered")
	}
}
func cloudAndGeo(t *testing.T) *ExitLookup {
	t.Helper()
	q := NewExitLookup()
	var providers []exitProvider
	for _, id := range []string{"cloudflare", "ipwho.is"} {
		for _, p := range q.providers {
			if p.id == id {
				p.tier = 0
				providers = append(providers, p)
			}
		}
	}
	q.providers = providers
	return q
}
func TestExitLookupPreservesIPAndRejectsWrongIPGeoEnrichment(t *testing.T) {
	q := cloudAndGeo(t)
	client := fixtureClient(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "www.cloudflare.com" {
			return geoResponse("ip=8.8.8.8\nloc=US\ncolo=LAX\n"), nil
		}
		return geoResponse(`{"ip":"1.1.1.1","country":"Australia","region":"Queensland","city":"Brisbane"}`), nil
	})
	v, code := q.Query(context.Background(), client, ExitQuery{Scope: "r"})
	if code != "" || v.IP != "8.8.8.8" || v.City != "" || v.GeoError == "" {
		t.Fatal("different exit geography was spliced", v, code)
	}
}
func TestExitLookupGeoCacheKeepsOriginalTimestampAndExpires(t *testing.T) {
	q := cloudAndGeo(t)
	q.cursor = 1
	now := time.Now()
	q.now = func() time.Time { return now }
	client := fixtureClient(func(*http.Request) (*http.Response, error) {
		return geoResponse(`{"ip":"8.8.8.8","country":"United States","region":"California","city":"Mountain View"}`), nil
	})
	first, code := q.Query(context.Background(), client, ExitQuery{Scope: "r"})
	if code != "" {
		t.Fatal(code)
	}
	cloud := fixtureClient(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "www.cloudflare.com" {
			return geoResponse("ip=8.8.8.8\nloc=US\n"), nil
		}
		r2 := geoResponse("")
		r2.StatusCode = 503
		return r2, nil
	})
	now = now.Add(23 * time.Hour)
	q.cursor = 0
	cached, code := q.Query(context.Background(), cloud, ExitQuery{Scope: "r"})
	if code != "" || cached.City != first.City || !cached.GeoAt.Equal(first.GeoAt) {
		t.Fatal(cached, code)
	}
	now = now.Add(2 * time.Hour)
	q.cursor = 0
	expired, code := q.Query(context.Background(), cloud, ExitQuery{Scope: "r"})
	if code != "" || expired.City != "" || expired.GeoError == "" {
		t.Fatal("cache self-renewed", expired, code)
	}
}
func TestExitLookupBrokenNodeDoesNotPoisonOtherRoutes(t *testing.T) {
	q := singleProvider(t, "ipwho.is")
	bad := fixtureClient(func(*http.Request) (*http.Response, error) {
		return nil, &Failure{Code: "node_connection_refused", Stage: "node_connection"}
	})
	for i := 0; i < 4; i++ {
		_, _ = q.Query(context.Background(), bad, ExitQuery{Scope: "bad"})
	}
	good := fixtureClient(func(*http.Request) (*http.Response, error) {
		return geoResponse(`{"ip":"8.8.8.8","country":"United States","region":"California","city":"Mountain View"}`), nil
	})
	v, code := q.Query(context.Background(), good, ExitQuery{Scope: "good"})
	if code != "" || v.IP == "" {
		t.Fatal(v, code)
	}
}
func TestExitProviderParsersRejectBadIPControlsAndCloudflareColo(t *testing.T) {
	for _, body := range []string{`{"ip":"127.0.0.1"}`, `{"ip":"192.168.1.2"}`, `{"ip":"bad"}`, `{"success":false,"ip":"8.8.8.8"}`, `{"ip":"8.8.8.8","error":{"reason":"bad"}}`} {
		if _, code := parseGeoProvider("ipwho.is", []byte(body)); code == "" {
			t.Fatal("invalid input accepted")
		}
	}
	value, code := parseGeoProvider("geojs", []byte(`{"ip":"8.8.8.8","asn":64512,"organization_name":"Unknown","country":"bad\ncountry"}`))
	if code != "" || value.Country != "" || value.ISP != "" || value.ASN != "" {
		t.Fatal(value, code)
	}
}
func TestExitLookupParentCancelNeverReturnsCachedOrCurrentSuccess(t *testing.T) {
	q := NewExitLookup()
	ctx, cancel := context.WithCancel(context.Background())
	client := fixtureClient(func(*http.Request) (*http.Response, error) { cancel(); return geoResponse(`{"ip":"8.8.8.8"}`), nil })
	value, code := q.Query(ctx, client, ExitQuery{Scope: "r"})
	if code != "operation_cancelled" || value.IP != "" {
		t.Fatal(value, code)
	}
}
func TestExitLookupCloudflare429CannotBeOverwrittenByAlternateHost(t *testing.T) {
	q := singleProvider(t, "cloudflare")
	calls := 0
	client := fixtureClient(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "www.cloudflare.com" {
			t.Error("429 bypassed with alias")
		}
		v := geoResponse("")
		v.StatusCode = 429
		v.Header.Set("Retry-After", "120")
		return v, nil
	})
	_, _ = q.Query(context.Background(), client, ExitQuery{Scope: "r"})
	_, _ = q.Query(context.Background(), client, ExitQuery{Scope: "r"})
	if calls != 1 {
		t.Fatal(calls)
	}
}

func TestLocateQueriesExactIPAndReusesRegionCache(t *testing.T) {
	q := singleProvider(t, "ipwho.is")
	calls := 0
	client := fixtureClient(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://ipwho.is/104.16.0.1" {
			t.Fatal("queried request source instead of selected CF IP")
		}
		return geoResponse(`{"ip":"104.16.0.1","country":"United States","country_code":"US","region":"California","city":"San Francisco"}`), nil
	})
	for i := 0; i < 2; i++ {
		v, code := q.Locate(context.Background(), client, "104.16.0.1", ExitQuery{Scope: "physical", Purpose: "cf_region"})
		if code != "" || v.IP != "104.16.0.1" || v.City != "San Francisco" {
			t.Fatal(v, code)
		}
	}
	if calls != 1 {
		t.Fatal("did not reuse exact IP region", calls)
	}
}

func TestLocateRejectsSourceIPReturnedByExactIPProvider(t *testing.T) {
	q := singleProvider(t, "ipwho.is")
	client := fixtureClient(func(*http.Request) (*http.Response, error) {
		return geoResponse(`{"ip":"8.8.8.8","country":"United States","region":"California","city":"Mountain View"}`), nil
	})
	value, code := q.Locate(context.Background(), client, "104.16.0.1", ExitQuery{Scope: "physical", Purpose: "cf_region"})
	if code == "" || value.IP != "" {
		t.Fatal("provider source IP passed as selected CF IP", value, code)
	}
}

func TestExitLookupDetailKeepsFailureReason(t *testing.T) {
	status := func(code int) Client {
		return fixtureClient(func(*http.Request) (*http.Response, error) {
			r := geoResponse("")
			r.StatusCode = code
			return r, nil
		})
	}
	_, code, reason := singleProvider(t, "ipwho.is").QueryDetail(context.Background(), status(525), ExitQuery{Scope: "r"})
	if code != "exit_http_failed" || reason != "525" {
		t.Fatal("http status dropped", code, reason)
	}
	node := fixtureClient(func(*http.Request) (*http.Response, error) {
		return nil, &Failure{Code: "node_ws_handshake_failed", Stage: "node_transport", Reason: "521"}
	})
	_, code, reason = singleProvider(t, "ipwho.is").QueryDetail(context.Background(), node, ExitQuery{Scope: "r"})
	if code != "node_ws_handshake_failed" || reason != "521" {
		t.Fatal("node reason dropped", code, reason)
	}
	ok := fixtureClient(func(*http.Request) (*http.Response, error) {
		return geoResponse(`{"ip":"8.8.8.8","country":"United States","country_code":"US","region":"California","city":"Mountain View"}`), nil
	})
	if _, code, reason = singleProvider(t, "ipwho.is").QueryDetail(context.Background(), ok, ExitQuery{Scope: "r"}); code != "" || reason != "" {
		t.Fatal("success carried reason", code, reason)
	}
}
