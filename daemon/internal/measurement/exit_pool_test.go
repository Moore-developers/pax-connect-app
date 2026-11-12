package measurement

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestExitPoolBoundsActualRequestsAcrossClients(t *testing.T) {
	lookup := NewExitLookup()
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	entered := make(chan struct{}, 32)
	client := fixtureClient(func(r *http.Request) (*http.Response, error) {
		entered <- struct{}{}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, _ = lookup.Query(ctx, client, ExitQuery{Scope: "shared-route-and-physical"})
		}()
	}
	for i := 0; i < 8; i++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("did not dispatch initial requests")
		}
	}
	select {
	case <-entered:
		t.Fatal("actual HTTP requests exceeded global default of eight")
	case <-time.After(900 * time.Millisecond):
	}
}

func TestExitPoolQueueWaitDoesNotConsumeExecutionBudget(t *testing.T) {
	q := NewExitLookup()
	q.SetLimit(1, 0)
	q.queryTimeout = 250 * time.Millisecond
	q.hedgeDelay = time.Second
	holding, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = q.Query(holding, fixtureClient(func(r *http.Request) (*http.Response, error) {
			entered <- struct{}{}
			<-r.Context().Done()
			return nil, r.Context().Err()
		}), ExitQuery{Scope: "holder"})
	}()
	<-entered
	result := make(chan string, 1)
	go func() {
		_, code := q.Query(context.Background(), fixtureClient(func(r *http.Request) (*http.Response, error) {
			timer := time.NewTimer(150 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-r.Context().Done():
				return nil, r.Context().Err()
			case <-timer.C:
			}
			return geoResponse(`{"ip":"8.8.8.8","country":"United States","region":"California","city":"Mountain View"}`), nil
		}), ExitQuery{Scope: "waiter", Purpose: "physical_ip"})
		result <- code
	}()
	time.Sleep(150 * time.Millisecond)
	cancel()
	<-done
	if code := <-result; code != "" {
		t.Fatal("queue consumed execution budget", code)
	}
	if q.Snapshot().InFlight != 0 || q.Snapshot().Queued != 0 {
		t.Fatal("lease leaked")
	}
}
func TestExitPoolLimitsFailedHTTPRequestsAndStopsOnNodeFault(t *testing.T) {
	for _, node := range []bool{false, true} {
		q := NewExitLookup()
		calls := 0
		client := fixtureClient(func(*http.Request) (*http.Response, error) {
			calls++
			if node {
				return nil, &Failure{Code: "node_connection_reset", Stage: "node_connection"}
			}
			v := geoResponse("")
			v.StatusCode = 503
			return v, nil
		})
		_, code := q.Query(context.Background(), client, ExitQuery{Scope: "r"})
		if node {
			if calls != 1 || code != "node_connection_reset" {
				t.Fatal("node failure retried across services", calls, code)
			}
		} else if calls != 3 {
			t.Fatal("failure retry was not bounded to three requests", calls)
		}
		if q.Snapshot().InFlight != 0 {
			t.Fatal("failure left a lease")
		}
	}
}
func TestExitPoolPhysicalPurposeDoesNotSpendRequestsOnGeography(t *testing.T) {
	q := singleProvider(t, "cloudflare")
	calls := 0
	v, code := q.Query(context.Background(), fixtureClient(func(*http.Request) (*http.Response, error) { calls++; return geoResponse("ip=8.8.8.8\nloc=US\n"), nil }), ExitQuery{Scope: "physical", Purpose: "physical_ip"})
	if calls != 1 || code != "" || v.IP != "8.8.8.8" || v.GeoError != "" {
		t.Fatal(calls, code, v.GeoError)
	}
}
func TestExitPoolBusinessRateErrorWaitsUntilUTCReset(t *testing.T) {
	q := singleProvider(t, "ipapi.is")
	now := time.Now().UTC()
	q.now = func() time.Time { return now }
	calls := 0
	client := fixtureClient(func(*http.Request) (*http.Response, error) {
		calls++
		return geoResponse(`{"error":"Anonymous quota exceeded","error_code":"ERR_FREE_TIER_EXHAUSTED"}`), nil
	})
	_, code := q.Query(context.Background(), client, ExitQuery{Scope: "r"})
	if code != "exit_rate_limited" {
		t.Fatal(code)
	}
	now = now.Add(61 * time.Second)
	_, _ = q.Query(context.Background(), client, ExitQuery{Scope: "other"})
	if calls != 1 {
		t.Fatal("daily quota restarted after sixty seconds")
	}
}
func TestExitPoolFIFOAndCancelledWaiterDoNotBlockFollowingRequests(t *testing.T) {
	q := NewExitLookup()
	q.SetLimit(1, 0)
	q.hedgeDelay = time.Second
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	entered := make(chan int, 8)
	var workers sync.WaitGroup
	start := func(id int, parent context.Context) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, _ = q.Query(parent, fixtureClient(func(r *http.Request) (*http.Response, error) {
				entered <- id
				select {
				case <-release:
				case <-r.Context().Done():
					return nil, r.Context().Err()
				}
				return geoResponse(`{"ip":"8.8.8.8","ipAddress":"8.8.8.8","country":"United States","countryName":"United States","region":"California","regionName":"California","city":"Mountain View","cityName":"Mountain View"}`), nil
			}), ExitQuery{Scope: "fifo", Purpose: "physical_ip"})
		}()
	}
	defer func() { cancel(); workers.Wait() }()
	start(0, ctx)
	<-entered
	cancelled, stop := context.WithCancel(ctx)
	start(1, cancelled)
	wait := func(count int) {
		deadline := time.Now().Add(time.Second)
		for q.Snapshot().Queued != count && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if q.Snapshot().Queued != count {
			t.Fatal("queue did not converge", count)
		}
	}
	wait(1)
	start(2, ctx)
	wait(2)
	start(3, ctx)
	wait(3)
	stop()
	wait(2)
	release <- struct{}{}
	if got := <-entered; got != 2 {
		t.Fatal("FIFO order changed", got)
	}
	release <- struct{}{}
	if got := <-entered; got != 3 {
		t.Fatal("cancel blocked the next waiter", got)
	}
	release <- struct{}{}
}

func TestExitPoolChargesEachObservedNATRequestOnce(t *testing.T) {
	q := singleProvider(t, "ip.sb")
	clock := time.Now()
	q.now = func() time.Time { clock = clock.Add(time.Nanosecond); return clock }
	client := fixtureClient(func(*http.Request) (*http.Response, error) { return geoResponse(`{"ip":"8.8.8.8"}`), nil })
	for _, scope := range []string{"a", "b", "c"} {
		_, code := q.Query(context.Background(), client, ExitQuery{Scope: scope, Purpose: "physical_ip"})
		if code != "" {
			t.Fatal(code)
		}
	}
	q.mu.Lock()
	known := q.health["ip.sb/8.8.8.8"]
	calls := known.calls
	recent := len(known.recent)
	q.mu.Unlock()
	if calls != 3 || recent != 3 {
		t.Fatalf("three actual requests charged %d times", calls)
	}
}
func TestExitPoolQuotaResetUsesUTCInLocalTimezone(t *testing.T) {
	q := singleProvider(t, "ipapi.is")
	now := time.Date(2026, 10, 18, 1, 0, 0, 0, time.FixedZone("CST", 8*3600))
	q.now = func() time.Time { return now }
	_, _ = q.Query(context.Background(), fixtureClient(func(*http.Request) (*http.Response, error) {
		return geoResponse(`{"error":"quota","error_code":"ERR_FREE_TIER_EXHAUSTED"}`), nil
	}), ExitQuery{Scope: "r"})
	q.mu.Lock()
	until := q.health["ipapi.is/unknown"].until
	q.mu.Unlock()
	want := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	if !until.Equal(want) {
		t.Fatalf("UTC reset shifted: got %v want %v", until, want)
	}
}

func TestExitPoolSourceChangesRebindWithoutUsingGeoTargetAsSource(t *testing.T) {
	q := singleProvider(t, "ipwho.is")
	ip := "8.8.8.8"
	client := fixtureClient(func(*http.Request) (*http.Response, error) { return geoResponse(`{"ip":"` + ip + `"}`), nil })
	for _, next := range []string{"8.8.8.8", "1.1.1.1"} {
		ip = next
		_, code := q.Query(context.Background(), client, ExitQuery{Scope: "same", Purpose: "physical_ip"})
		if code != "" {
			t.Fatal(code)
		}
	}
	q.mu.Lock()
	group := q.sources["ipwho.is/same"]
	q.mu.Unlock()
	if group != "1.1.1.1" {
		t.Fatal("new observed source was not rebound", group)
	}
	// Geo enrichment is an explicit query about an arbitrary target, not self observation.
	lease, code := q.acquire(context.Background(), &exitWaiter{scope: "same", tried: map[string]bool{}}, false)
	if code != "" {
		t.Fatal(code)
	}
	ip = "9.9.9.9"
	_, _, _ = q.request(context.Background(), client, lease, "same", ip)
	q.mu.Lock()
	group = q.sources["ipwho.is/same"]
	q.mu.Unlock()
	if group != "1.1.1.1" {
		t.Fatal("geo target replaced actual source")
	}
}

func TestExitPoolExpiredSourceStateIsReclaimed(t *testing.T) {
	q := singleProvider(t, "geojs")
	clock := time.Now()
	q.now = func() time.Time { return clock }
	q.queueTimeout = 20 * time.Millisecond
	for i := 0; i < 300; i++ {
		clock = clock.Add(25 * time.Hour)
		client := fixtureClient(func(*http.Request) (*http.Response, error) {
			return geoResponse(fmt.Sprintf(`{"ip":"11.0.%d.%d"}`, i/250, i%250+1)), nil
		})
		_, code := q.Query(context.Background(), client, ExitQuery{Scope: fmt.Sprintf("expired-%d", i), Purpose: "physical_ip"})
		if code != "" {
			t.Fatalf("healthy source %d rejected after expired state accumulated: %s", i+1, code)
		}
		if len(q.health) > 256 {
			t.Fatal("health state exceeded its bound")
		}
	}
}

func TestExitPoolFreshScopesShareConservativeSourceLimits(t *testing.T) {
	q := singleProvider(t, "ip.sb")
	clock := time.Now()
	q.now = func() time.Time { return clock }
	calls := 0
	client := fixtureClient(func(*http.Request) (*http.Response, error) { calls++; return geoResponse(`{"ip":"8.8.8.8"}`), nil })
	for i := 0; i < 5; i++ {
		if _, code := q.Query(context.Background(), client, ExitQuery{Scope: fmt.Sprint(i), Purpose: "physical_ip"}); code != "" {
			t.Fatal(code)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, _ = q.Query(ctx, client, ExitQuery{Scope: "sixth-new-route", Purpose: "physical_ip"})
	if calls != 5 {
		t.Fatalf("fresh route scopes bypassed five-per-second source limit: %d requests", calls)
	}
	clock = clock.Add(time.Second)
	if _, code := q.Query(context.Background(), client, ExitQuery{Scope: "after-reset", Purpose: "physical_ip"}); code != "" {
		t.Fatal(code)
	}
	if calls != 6 {
		t.Fatal("source guard never recovered", calls)
	}
}

func TestExitPoolNodeFaultStopsExactIPEnrichment(t *testing.T) {
	q := NewExitLookup()
	var selected []exitProvider
	for _, id := range []string{"cloudflare", "ipwho.is", "ip.sb"} {
		for _, p := range q.providers {
			if p.id == id {
				p.tier = 0
				selected = append(selected, p)
			}
		}
	}
	q.providers = selected
	calls := 0
	client := fixtureClient(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return geoResponse("ip=8.8.8.8\nloc=US\n"), nil
		}
		return nil, &Failure{Code: "node_connection_reset", Stage: "node_connection"}
	})
	value, code := q.Query(context.Background(), client, ExitQuery{Scope: "enrich"})
	if calls != 2 {
		t.Fatalf("node fault during enrichment retried another API: %d requests", calls)
	}
	if code != "" || value.IP != "8.8.8.8" || value.GeoError != "exit_location_unavailable" {
		t.Fatal("enrichment fault discarded the observed IP", code)
	}
}

func TestExitPoolBusyProviderRemainsEligibleBesideCoolingProvider(t *testing.T) {
	q := NewExitLookup()
	q.providers = q.providers[:2]
	q.providers[0].slots = 1
	q.health[q.providers[1].id].until = time.Now().Add(time.Minute)
	holding, release := context.WithCancel(context.Background())
	entered := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = q.Query(holding, fixtureClient(func(r *http.Request) (*http.Response, error) {
			entered <- struct{}{}
			<-r.Context().Done()
			return nil, r.Context().Err()
		}), ExitQuery{Scope: "holder", Purpose: "physical_ip"})
	}()
	defer func() { release(); <-done }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan string, 1)
	go func() {
		_, code := q.Query(ctx, fixtureClient(func(*http.Request) (*http.Response, error) { return geoResponse(`{"ip":"8.8.8.8"}`), nil }), ExitQuery{Scope: "waiting", Purpose: "physical_ip"})
		result <- code
	}()
	select {
	case code := <-result:
		t.Fatal("busy healthy provider rejected as unavailable", code)
	case <-time.After(30 * time.Millisecond):
	}
	release()
	<-done
	if code := <-result; code != "" {
		t.Fatal("released healthy capacity was not used", code)
	}
}

func TestExitPoolLimitUsesCommittedRevisionOrder(t *testing.T) {
	q := NewExitLookup()
	q.SetLimit(2, 3)
	q.SetLimit(1, 2) // Earlier save finishes applying after the newer save.
	if q.Snapshot().ConfiguredLimit != 2 {
		t.Fatal("earlier policy replaced a later committed limit")
	}
	q.SetLimit(4, 4)
	if q.Snapshot().ConfiguredLimit != 4 {
		t.Fatal("newer policy was not applied")
	}
}

func TestExitPoolAnonymousGuardCoversIPv6AndDailyBudgets(t *testing.T) {
	for _, fixture := range []struct {
		id, ip string
		limit  int
	}{
		{"ip.sb", "2001:4860:4860::8888", 5},
		{"ipapi.is", "8.8.8.8", 30},
	} {
		t.Run(fixture.id, func(t *testing.T) {
			q := singleProvider(t, fixture.id)
			clock := time.Now().UTC()
			q.now = func() time.Time { return clock }
			calls := 0
			client := fixtureClient(func(*http.Request) (*http.Response, error) {
				calls++
				return geoResponse(`{"ip":"` + fixture.ip + `"}`), nil
			})
			for i := 0; i < fixture.limit; i++ {
				if _, code := q.Query(context.Background(), client, ExitQuery{Scope: fmt.Sprint(i), Purpose: "physical_ip"}); code != "" {
					t.Fatal(code)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			_, _ = q.Query(ctx, client, ExitQuery{Scope: "another-new-scope", Purpose: "physical_ip"})
			if calls != fixture.limit {
				t.Fatal("source discovery replenished anonymous budget", calls)
			}
		})
	}
}
