package measurement

import (
	"context"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"time"
)

var diagnosticOnlyProviders = []exitProvider{
	{id: "internetdb", endpoint: "https://internetdb.shodan.io/", slots: 2},
	{id: "ipx.al", endpoint: "https://ipx.al/api/report/", slots: 2},
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
	timeout := 5 * time.Second
	if id == "ipx.al" {
		timeout = 6 * time.Second
	}
	bounded, cancel := context.WithTimeout(ctx, timeout)
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
	if id == "internetdb" || id == "ipx.al" {
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
	finishCode := code
	if code == "provider_response_invalid" || code == "provider_ip_mismatch" {
		finishCode = "exit_response_invalid"
	}
	q.finish(lease, scope, ExitResult{}, finishCode, status, retry, ctx.Err() != nil)
	result.attempt.ErrorCode = code
	result.report.ProviderError = code
	result.attempt.HTTPStatus = status
	result.attempt.Duration = time.Since(started)
	return result
}
