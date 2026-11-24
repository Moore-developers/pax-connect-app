package measurement

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type diagnosticTestClient func(*http.Request) (*http.Response, error)

func (f diagnosticTestClient) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestNetworkProbeResponsesAndCancellation(t *testing.T) {
	client := diagnosticTestClient(func(r *http.Request) (*http.Response, error) {
		code := 200
		if strings.Contains(r.URL.Host, "api.") {
			code = 401
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	r := DiagnoseNetwork(context.Background(), client, "services")
	if len(r.Probes) != 2 {
		t.Fatal(r)
	}
	for _, p := range r.Probes {
		if p.Target != "https://api.anthropic.com/v1/models" && p.Target != "https://api.openai.com/v1/models" {
			t.Fatalf("service recheck requested a non-API target: %s", p.Target)
		}
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
	if len(v.Probes) != 11 || calls.Load() != 0 {
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
			if update.ProbesTotal != 2 {
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
	if len(result.Probes) != 2 {
		t.Fatal(len(result.Probes))
	}
	maxCompleted := 0
	for len(updates) > 0 {
		maxCompleted = max(maxCompleted, (<-updates).ProbesCompleted)
	}
	if maxCompleted != 2 {
		t.Fatal("settled count excludes failed/cancelled requests", maxCompleted)
	}
}
func TestNetworkProviderBodyAndRouteBoundEndpoint(t *testing.T) {
	var address string
	c := diagnosticTestClient(func(r *http.Request) (*http.Response, error) {
		address = r.URL.String()
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", networkReportLimit+1)))}, nil
	})
	v := NewExitLookup().fetchIntelligence(context.Background(), c, "ipwho.is", "203.0.113.24", "test").report
	if address != "https://ipwho.is/203.0.113.24" || v.ProviderError != "provider_response_invalid" {
		t.Fatal(address, v)
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
	if len(full.Probes) != 12 || len(hosts) != 4 {
		t.Fatalf("incomplete full capture: %d probes/%d targets", len(full.Probes), len(hosts))
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	result := DiagnoseNetwork(ctx, client, "services")
	if len(result.Probes) != 2 {
		t.Fatal("missing queued targets", len(result.Probes))
	}
	for _, p := range result.Probes {
		if p.State != "not_started" || p.ErrorCode != "diagnostic_budget_exhausted" {
			t.Fatalf("misreported budget as network failure: %+v", p)
		}
	}
}
