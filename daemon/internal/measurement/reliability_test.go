package measurement

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestExitLookupAcceptsColdEndpointWithinOverallBudget(t *testing.T) {
	lookup := NewExitLookup()
	client := fixtureClient(func(r *http.Request) (*http.Response, error) {
		timer := time.NewTimer(1650 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-timer.C:
		}
		return geoResponse(`{"success":true,"ip":"8.8.8.8","country":"United States","region":"California","city":"Mountain View"}`), nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	value, code := lookup.Query(ctx, client, ExitQuery{Scope: "cold-route"})
	if code != "" || value.IP == "" {
		t.Fatal("cold but healthy endpoint rejected inside total budget", code)
	}
}
func TestDownloadSlowSetupRetainsBoundedUsefulBodyWindow(t *testing.T) {
	client := fixtureClient(func(r *http.Request) (*http.Response, error) {
		timer := time.NewTimer(800 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-timer.C:
		}
		return &http.Response{StatusCode: 200, Body: &timedBody{ctx: r.Context(), remaining: 512 * 1024}}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2300*time.Millisecond)
	defer cancel()
	value, err := Download(ctx, client, DownloadBudget)
	if err != nil || value.Bytes != 512*1024 || value.Duration < time.Second || value.End != "budget_window" {
		t.Fatal("useful transfer thrown out after setup consumed budget", value.End, value.Bytes, value.Duration, err)
	}
}

func TestExitLookupSlowPreferredServiceDoesNotDelayHealthyAlternate(t *testing.T) {
	lookup := NewExitLookup()
	client := fixtureClient(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "ipwho.is" {
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		return geoResponse(`{"success":true,"ip":"8.8.8.8","country":"United States","region":"California","city":"Mountain View"}`), nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	value, code := lookup.Query(ctx, client, ExitQuery{Scope: "slow-primary"})
	if code != "" || value.IP == "" {
		t.Fatal("slow primary exhausted total instead of hedging", code)
	}
}
func TestExitLookupHedgedCancellationRemainsCancellation(t *testing.T) {
	lookup := NewExitLookup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 2)
	client := fixtureClient(func(r *http.Request) (*http.Response, error) {
		started <- struct{}{}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	done := make(chan string, 1)
	go func() { _, code := lookup.Query(ctx, client, ExitQuery{Scope: "cancelled"}); done <- code }()
	<-started
	cancel()
	select {
	case code := <-done:
		if code != "operation_cancelled" {
			t.Fatal(code)
		}
	case <-time.After(time.Second):
		t.Fatal("hedge workers not drained")
	}
}

func TestExitLookupWaitsForHealthyBusyProvidersRatherThanReportingUnavailable(t *testing.T) {
	lookup := NewExitLookup()
	holding, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{}, 10)
	var workers sync.WaitGroup
	block := fixtureClient(func(r *http.Request) (*http.Response, error) {
		entered <- struct{}{}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, _ = lookup.Query(holding, block, ExitQuery{Scope: "held", Purpose: "physical_ip"})
		}()
	}
	defer func() { cancel(); workers.Wait() }()
	for i := 0; i < 8; i++ {
		<-entered
	}
	client := fixtureClient(func(r *http.Request) (*http.Response, error) {
		return geoResponse(`{"success":true,"ip":"8.8.8.8","country":"United States","region":"California","city":"Mountain View"}`), nil
	})
	result := make(chan string, 1)
	ctx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	go func() { _, code := lookup.Query(ctx, client, ExitQuery{Scope: "new-route"}); result <- code }()
	select {
	case code := <-result:
		cancel()
		t.Fatal("temporary busy slots reported as dead services", code)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case code := <-result:
		if code != "" {
			t.Fatal("did not use released healthy provider", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("busy retry did not resume")
	}
}

func TestExitLookupRetainsNodeFailureWithoutCoolingServices(t *testing.T) {
	for _, failure := range []*Failure{
		{Code: "node_connection_reset", Stage: "node_connection"},
		{Code: "node_ws_handshake_failed", Stage: "node_transport"},
	} {
		lookup := NewExitLookup()
		client := fixtureClient(func(r *http.Request) (*http.Response, error) { return nil, failure })
		for i := 0; i < 4; i++ {
			_, code := lookup.Query(context.Background(), client, ExitQuery{Scope: "same-route"})
			if code != failure.Code {
				t.Fatalf("node evidence lost: got %s want %s", code, failure.Code)
			}
		}
		for _, state := range lookup.health {
			if state.failures != 0 || !state.until.IsZero() {
				t.Fatal("node failure cooled exit service")
			}
		}
	}
}

func TestExitLookupBusyWaitHonorsCancellationAndDeadline(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		lookup := NewExitLookup()
		for _, p := range exitProviders {
			lookup.health[p.id] = &providerHealth{inflight: 2}
		}
		lookup.queueTimeout = 250 * time.Millisecond
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if cancelled {
			cancel()
		}
		_, code := lookup.Query(ctx, fixtureClient(func(r *http.Request) (*http.Response, error) {
			t.Error("busy service contacted")
			return nil, context.Canceled
		}), ExitQuery{Scope: "busy"})
		cancel()
		want := "exit_queue_timeout"
		if cancelled {
			want = "operation_cancelled"
		}
		if code != want {
			t.Fatalf("got %s want %s", code, want)
		}
	}
}

func TestExitLookupShortRateGateWaitsWithoutRetryingFailedRequests(t *testing.T) {
	lookup := NewExitLookup()
	now := time.Now()
	for _, p := range exitProviders {
		lookup.health[p.id] = &providerHealth{until: now.Add(time.Minute)}
	}
	lookup.health["ip.sb"] = &providerHealth{}
	lookup.health["ip.sb/unknown"] = &providerHealth{recent: []time.Time{now, now, now, now, now}}
	calls := 0
	client := fixtureClient(func(r *http.Request) (*http.Response, error) {
		calls++
		return geoResponse(`{"ip":"8.8.8.8","country":"United States","region":"California","city":"Mountain View"}`), nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	value, code := lookup.Query(ctx, client, ExitQuery{Scope: "rate-gated"})
	if code != "" || value.IP == "" || calls != 1 {
		t.Fatal("short rate gate treated as unavailable", code, calls)
	}
}
