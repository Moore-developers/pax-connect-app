package measurement

import (
	"context"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"time"
)

const networkReportLimit = 500 * 1024

var diagnosticOnlyProviders = []exitProvider{
	{id: "internetdb", endpoint: "https://internetdb.shodan.io/", slots: 2},
	{id: "db-ip", endpoint: "https://api.db-ip.com/v2/free/", slots: 1, daily: 500},
	{id: "ip2location.io", endpoint: "https://api.ip2location.io/", slots: 1, daily: 1000, utcDay: true},
	{id: "proxycheck.io", endpoint: "https://proxycheck.io/v3/", slots: 1, daily: 100},
	{id: "iplocate", endpoint: "https://www.iplocate.io/api/lookup/", slots: 1},
	{id: "ripestat", endpoint: "https://stat.ripe.net/data/network-info/data.json", slots: 1},
}

func diagnosticProvider(id string) (exitProvider, bool) {
	for _, p := range exitProviders {
		if p.id == id {
			return p, true
		}
	}
	for _, p := range diagnosticOnlyProviders {
		if p.id == id {
			return p, true
		}
	}
	return exitProvider{}, false
}

type intelligenceResult struct {
	report  NetworkDiagnosticReport
	attempt NetworkDiagnosticProvider
}

// fetchIntelligence shares admission, quotas and cooldowns with exit observation.
// Exact-IP lookups never teach the quota owner that the queried IP is the caller.
func (q *ExitLookup) fetchIntelligence(ctx context.Context, client Client, id, ip, scope string) intelligenceResult {
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addr := netip.MustParseAddr(ip) // validated by DiagnoseIP
	family := "ipv4"
	if addr.Is6() {
		family = "ipv6"
	}
	result := intelligenceResult{attempt: NetworkDiagnosticProvider{ID: id, QueryIP: ip, Family: family, At: time.Now(), State: "failed"}}
	started := time.Now()
	w := &exitWaiter{provider: id, scope: scope, tried: map[string]bool{}}
	lease, code := q.acquire(bounded, w, false)
	if code != "" {
		result.attempt.ErrorCode = code
		result.report.ProviderError = code
		result.attempt.Duration = time.Since(started)
		return result
	}
	p := lease.provider
	target := providerTarget(p, ip)
	if isRouteComparison(id) {
		target = comparisonTarget(p, ip)
	}
	if id == "internetdb" {
		target = p.endpoint + ip
	}
	req, _ := http.NewRequestWithContext(bounded, http.MethodGet, target, nil)
	req.Header.Set("User-Agent", "PaxConnect/1.0")
	req.Header.Set("Accept", "application/json")
	response, err := client.Do(req)
	status := 0
	var retry time.Duration
	var body []byte
	if err != nil {
		code = Classify(err, false).Code
	} else {
		status = response.StatusCode
		retry = retryDuration(response.Header.Get("Retry-After"), q.now())
		body, err = io.ReadAll(io.LimitReader(response.Body, networkReportLimit+1))
		_ = response.Body.Close()
		switch {
		case err != nil:
			code = Classify(err, false).Code
		case len(body) > networkReportLimit:
			code = "provider_response_invalid"
		case status == http.StatusNotFound && id == "internetdb":
			result.attempt.State = "not_indexed"
			result.attempt.Capabilities = []string{"exposure"}
		case id == "ip2location.io" && status == http.StatusUnauthorized && ip2LocationQuotaExceeded(body):
			retry = nextUTCDay(q.now())
			code = "exit_rate_limited"
		case status != http.StatusOK:
			code = "provider_http_" + strconv.Itoa(status)
		default:
			if businessStatus, wait, businessCode := providerBusinessError(id, body, q.now()); businessCode != "" {
				status, retry, code = businessStatus, wait, businessCode
			} else {
				result.report = parseIntelligence(id, body, addr)
				code = result.report.ProviderError
				if code == "" {
					result.attempt.State = "observed"
				}
			}
		}
	}
	if bounded.Err() != nil {
		code = "provider_timeout"
		if ctx.Err() == context.Canceled {
			code = "operation_cancelled"
		}
		result.attempt.State = "failed"
	}
	// Provider authentication is global, while 403 remains path-specific.
	finishCode, finishStatus := code, status
	if code == "exit_rate_limited" {
		finishStatus = http.StatusTooManyRequests
	}
	if code == "provider_response_invalid" || code == "provider_ip_mismatch" {
		finishCode = "exit_response_invalid"
	}
	q.finish(lease, scope, ExitResult{}, finishCode, finishStatus, retry, ctx.Err() != nil)
	result.attempt.ErrorCode = code
	if result.attempt.State == "observed" && isRouteComparison(id) {
		setComparisonCapabilities(&result)
	}
	result.report.ProviderError = code
	result.attempt.HTTPStatus = status
	result.attempt.Duration = time.Since(started)
	return result
}
