package measurement

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// NetworkDiagnosticSource is one provider's own geolocation evidence.
type NetworkDiagnosticSource struct{ Name, Country, City, ASN, Org, ProviderDate, ProviderID string }

// NetworkDiagnosticFact is an attributed, bounded third-party observation.
type NetworkDiagnosticFact struct{ Group, Label, Value, Source, State, ProviderID string }

// NetworkDiagnosticProvider preserves an exact-IP attempt and its acquisition channel.
type NetworkDiagnosticProvider struct {
	ID, QueryIP, Family, State, ErrorCode, FallbackReason string
	Capabilities, FallbackFor                             []string
	HTTPStatus                                            int
	Duration                                              time.Duration
	At                                                    time.Time
}

// NetworkDiagnosticProbe records one actual request, including non-success HTTP responses.
type NetworkDiagnosticProbe struct {
	Target                     string
	Attempt, HTTPStatus        int
	Duration                   time.Duration
	ErrorCode                  string
	ExpectedResponse           bool
	At                         time.Time
	State, Stage, RedirectHost string
}

// NetworkDiagnosticReport contains no credentials, headers or upstream JSON.
type NetworkDiagnosticReport struct {
	Score                                    *int
	ScoreModel                               string
	ScoreCeiling                             int
	IPType, Timezone, Country, ProviderError string
	IntelAnswered, IntelTotal                int
	Sources                                  []NetworkDiagnosticSource
	Facts                                    []NetworkDiagnosticFact
	Probes                                   []NetworkDiagnosticProbe
	Providers                                []NetworkDiagnosticProvider
}

type diagnosticTarget struct {
	url   string
	count int
}

func diagnosticTargets(mode string) []diagnosticTarget {
	services := []diagnosticTarget{
		{"https://chatgpt.com/", 1}, {"https://api.openai.com/v1/models", 1},
		{"https://claude.ai/", 1}, {"https://api.anthropic.com/v1/models", 1},
		{"https://grok.com/", 1}, {"https://www.perplexity.ai/", 1},
		{"https://gemini.google.com/", 1}, {"https://www.tiktok.com/", 1},
		{"https://www.facebook.com/", 1}, {"https://x.com/", 1},
		{"https://store.steampowered.com/", 1}, {"https://github.com/", 1},
	}
	if mode == "services" {
		return services
	}
	targets := []diagnosticTarget{{"https://www.google.com/generate_204", 5}, {"https://www.cloudflare.com/cdn-cgi/trace", 5}}
	if mode == "full" {
		return append(targets, services...)
	}
	return append(targets, services[2:4]...)
}

// DiagnoseNetwork probes only fixed public endpoints through the injected route client.
// Workers write separate result slots; cancellation is joined before returning.
func DiagnoseNetwork(ctx context.Context, client Client, mode string) NetworkDiagnosticReport {
	targets := diagnosticTargets(mode)
	total := 0
	for _, target := range targets {
		total += target.count
	}
	var completed atomic.Int32
	ObserveDiagnostic(ctx, DiagnosticProgress{ProbesTotal: total})
	results := make([][]NetworkDiagnosticProbe, len(targets))
	var workers sync.WaitGroup
	slots := make(chan struct{}, 2)
	for i, target := range targets {
		workers.Add(1)
		go func(i int, target diagnosticTarget) {
			defer workers.Done()
			acquired := false
			select {
			case slots <- struct{}{}:
				acquired = true
			case <-ctx.Done():
			}
			// Even a cancelled queue slot produces evidence for every intended request.
			if acquired {
				defer func() { <-slots }()
			}
			for attempt := 1; attempt <= target.count; attempt++ {
				if ctx.Err() != nil {
					state, code := "cancelled", "diagnostic_cancelled"
					if ctx.Err() == context.DeadlineExceeded {
						state, code = "not_started", "diagnostic_budget_exhausted"
					}
					results[i] = append(results[i], NetworkDiagnosticProbe{Target: target.url, Attempt: attempt, At: time.Now(), State: state, Stage: "budget", ErrorCode: code})
					ObserveDiagnostic(ctx, DiagnosticProgress{ProbesCompleted: int(completed.Add(1)), ProbesTotal: total})
					continue
				}
				results[i] = append(results[i], diagnosticRequest(ctx, client, target.url, attempt))
				ObserveDiagnostic(ctx, DiagnosticProgress{ProbesCompleted: int(completed.Add(1)), ProbesTotal: total})
			}
		}(i, target)
	}
	workers.Wait()
	var report NetworkDiagnosticReport
	for _, samples := range results {
		report.Probes = append(report.Probes, samples...)
	}
	return report
}

func diagnosticRequest(ctx context.Context, client Client, target string, attempt int) NetworkDiagnosticProbe {
	bounded, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	sample := NetworkDiagnosticProbe{Target: target, Attempt: attempt, At: time.Now(), State: "failed", Stage: "connection"}
	request, err := http.NewRequestWithContext(bounded, http.MethodGet, target, nil)
	if err != nil {
		sample.ErrorCode = "test_target_invalid"
		return sample
	}
	request.Header.Set("User-Agent", "PaxConnect/1.0")
	started := time.Now()
	response, err := client.Do(request)
	sample.Duration = time.Since(started)
	if err != nil {
		failure := Classify(err, false)
		sample.ErrorCode = failure.Code
		if strings.Contains(failure.Code, "dns_") {
			sample.Stage = "dns"
		}
		if strings.Contains(failure.Code, "tls_") {
			sample.Stage = "tls"
		}
		if ctx.Err() == context.Canceled {
			sample.State = "cancelled"
			sample.ErrorCode = "diagnostic_cancelled"
		}
		return sample
	}
	sample.HTTPStatus = response.StatusCode
	sample.State = "responded"
	sample.Stage = "response"
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		if location, err := url.Parse(response.Header.Get("Location")); err == nil {
			sample.RedirectHost = diagnosticText(location.Hostname())
		}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	_ = response.Body.Close()
	sample.ExpectedResponse = response.StatusCode >= 200 && response.StatusCode < 400
	if response.StatusCode >= 400 {
		sample.ErrorCode = "target_http_" + strconv.Itoa(response.StatusCode)
	}
	return sample
}
