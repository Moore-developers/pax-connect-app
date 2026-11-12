package measurement

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionLatencySeparatesColdAndThreeWarmMedian(t *testing.T) {
	var calls atomic.Int32
	delays := []time.Duration{5 * time.Millisecond, 20 * time.Millisecond, 80 * time.Millisecond, 40 * time.Millisecond}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index := int(calls.Add(1)) - 1
		if index >= len(delays) {
			t.Error("unbounded samples")
			w.WriteHeader(500)
			return
		}
		time.Sleep(delays[index])
		w.WriteHeader(204)
	}))
	defer server.Close()
	sample, err := SessionLatency(context.Background(), server.Client(), []string{server.URL})
	if err != nil || calls.Load() != 4 || sample.Cold <= 0 || sample.StableSamples != 3 || sample.Stable < 30*time.Millisecond || sample.Stable > 70*time.Millisecond || sample.StableError != "" {
		t.Fatal(sample, err, calls.Load())
	}
}
func TestSessionLatencyKeepsColdWithoutInventingStableOnDeadline(t *testing.T) {
	calls := 0
	client := fixtureClient(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	sample, err := SessionLatency(ctx, client, []string{"https://fixture.invalid"})
	if err != nil || sample.Cold <= 0 || sample.Stable != 0 || sample.StableSamples != 0 || sample.StableError != "latency_samples_incomplete" {
		t.Fatal(sample, err)
	}
}

func TestSessionLatencyExplicitCancelDoesNotBecomeSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := fixtureClient(func(req *http.Request) (*http.Response, error) {
		cancel()
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	if _, err := SessionLatency(ctx, client, []string{"https://fixture.invalid"}); err != context.Canceled {
		t.Fatal(err)
	}
}
func TestSessionLatencyRejectsRedirect(t *testing.T) {
	client := fixtureClient(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 302, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	if sample, err := SessionLatency(context.Background(), client, []string{"https://fixture.invalid"}); err == nil || sample.Cold != 0 || sample.FirstError != "target_http_failed" {
		t.Fatal(sample, err)
	}
}

func TestSessionLatencyFallbackKeepsOneTargetAndReusedResponses(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/bad" {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	sample, err := SessionLatency(context.Background(), server.Client(), []string{server.URL + "/bad", server.URL + "/good"})
	if err != nil || sample.Target != server.URL+"/good" || sample.StableSamples != 3 || sample.Stable <= 0 || calls.Load() != 5 {
		t.Fatal(sample, err, calls.Load())
	}
}
func TestSessionLatencyDoesNotCallFreshConnectionsStable(t *testing.T) {
	client := fixtureClient(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	sample, err := SessionLatency(context.Background(), client, []string{"https://fixture.invalid"})
	if err != nil || sample.Cold <= 0 || sample.Stable != 0 || sample.StableSamples != 0 || sample.StableError != "latency_samples_incomplete" {
		t.Fatal(sample, err)
	}
}

// Inject an expired deadline exactly after the last valid response, without
// timing-dependent sleeps or changing the measurement clock.
type responseDeadline struct{ context.Context }

func (c responseDeadline) Err() error {
	if c.Context.Err() != nil {
		return context.DeadlineExceeded
	}
	return nil
}
func TestSessionLatencyThirdCompletedResponseAtDeadlineKeepsMedian(t *testing.T) {
	base, expire := context.WithCancel(context.Background())
	defer expire()
	ctx := responseDeadline{base}
	calls := 0
	client := fixtureClient(func(req *http.Request) (*http.Response, error) {
		calls++
		if trace := httptrace.ContextClientTrace(req.Context()); trace != nil && trace.GotConn != nil {
			trace.GotConn(httptrace.GotConnInfo{Reused: calls > 1})
		}
		if calls == 4 {
			expire()
		}
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	sample, err := SessionLatency(ctx, client, []string{"https://fixture.invalid"})
	if err != nil || sample.StableSamples != 3 || sample.Stable <= 0 || sample.StableError != "" {
		t.Fatal("complete third response lost median", sample, err)
	}
}
