package singbox

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/sagernet/sing-box/adapter"
)

func TestRouteGateClosesPendingTCPAndUDPOnlyOnFailedFixedRoute(t *testing.T) {
	gate := &routeGate{}
	gate.configure(map[string]bool{"a": true, "b": true})
	var affected, healthy, global atomic.Int32
	var pending []context.Context
	for _, network := range []string{"tcp", "udp"} {
		ctx, release, err := gate.acquire(context.Background(), "a", func() error {
			affected.Add(1)
			return nil
		})
		if err != nil {
			t.Fatal(network, err)
		}
		t.Cleanup(release)
		pending = append(pending, ctx)
	}
	ctxB, releaseB, err := gate.acquire(context.Background(), "b", func() error { healthy.Add(1); return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(releaseB)
	ctxGlobal, releaseGlobal, err := gate.acquire(context.Background(), "proxy", func() error { global.Add(1); return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(releaseGlobal)
	gate.setBlocked("a", true)
	for _, ctx := range pending {
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatal("matched flow survived the block before native tracking")
		}
	}
	if affected.Load() != 2 || healthy.Load() != 0 || global.Load() != 0 || ctxB.Err() != nil || ctxGlobal.Err() != nil {
		t.Fatal("block crossed its fixed-route boundary", affected.Load(), healthy.Load(), global.Load())
	}
	if _, release, err := gate.acquire(context.Background(), "a", nil); !errors.Is(err, errFixedRouteUnavailable) {
		release()
		t.Fatal("new fixed-route flow was admitted while blocked", err)
	}
	gate.setBlocked("a", false)
	ctx, release, err := gate.acquire(context.Background(), "a", nil)
	if err != nil || ctx.Err() != nil {
		t.Fatal("recovered fixed route remained blocked", err)
	}
	release()
	release()
}

func TestRouteGateCloseMaySynchronouslyReleaseWithoutDeadlock(t *testing.T) {
	for _, action := range []string{"block", "reset"} {
		t.Run(action, func(t *testing.T) {
			gate := &routeGate{}
			gate.configure(map[string]bool{"a": true})
			var release func()
			var closed atomic.Int32
			ctx, free, err := gate.acquire(context.Background(), "a", func() error {
				closed.Add(1)
				release()
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			release = free
			finished := make(chan struct{})
			go func() {
				if action == "block" {
					gate.setBlocked("a", true)
				} else {
					gate.reset()
				}
				close(finished)
			}()
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("closing the connection deadlocked its release")
			}
			if closed.Load() != 1 || !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatal("pending connection was not canceled and closed")
			}
			release()
			gate.reset()
			if len(gate.candidates(time.Now())) != 0 {
				t.Fatal("reset retained protection candidates")
			}
		})
	}
}

func TestRouteGateCandidatesIncludeQuietLeasesRecentAttemptsAndBlockedRoutes(t *testing.T) {
	gate := &routeGate{}
	gate.configure(map[string]bool{"quiet": true, "recent": true, "blocked": true, "unused": true})
	_, quietRelease, err := gate.acquire(context.Background(), "quiet", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(quietRelease)
	_, recentRelease, err := gate.acquire(context.Background(), "recent", nil)
	if err != nil {
		t.Fatal(err)
	}
	recentRelease()
	now := time.Now()
	gate.mu.Lock()
	gate.recent["quiet"] = now.Add(-2 * time.Minute)
	gate.recent["recent"] = now.Add(-59 * time.Second)
	gate.recent["blocked"] = now.Add(-2 * time.Minute)
	gate.mu.Unlock()
	gate.setBlocked("blocked", true)
	if got, want := gate.candidates(now), map[string]bool{"quiet": false, "recent": false, "blocked": true}; !reflect.DeepEqual(got, want) {
		t.Fatal("wrong active protection scope", got, want)
	}
	quietRelease()
	if got, want := gate.candidates(now.Add(time.Second)), map[string]bool{"blocked": true}; !reflect.DeepEqual(got, want) {
		t.Fatal("expired attempts or released quiet connections stayed active", got, want)
	}
	before := gate.environment()
	gate.configure(map[string]bool{"recent": true})
	if len(gate.candidates(now.Add(time.Second))) != 0 || gate.environment() == before {
		t.Fatal("new configuration retained removed blocked route or cache generation")
	}
}

func TestRouteGateConcurrentAdmissionAndBlockCannotLeaveLiveLease(t *testing.T) {
	gate := &routeGate{}
	gate.configure(map[string]bool{"a": true})
	type admission struct {
		ctx    context.Context
		free   func()
		err    error
		closed *atomic.Int32
	}
	const workers = 64
	results := make(chan admission, workers)
	start := make(chan struct{})
	var done sync.WaitGroup
	done.Add(workers + 1)
	for i := 0; i < workers; i++ {
		go func() {
			defer done.Done()
			<-start
			closed := &atomic.Int32{}
			ctx, free, err := gate.acquire(context.Background(), "a", func() error { closed.Add(1); return nil })
			results <- admission{ctx: ctx, free: free, err: err, closed: closed}
		}()
	}
	go func() {
		defer done.Done()
		<-start
		gate.setBlocked("a", true)
	}()
	close(start)
	done.Wait()
	close(results)
	for result := range results {
		if result.err == nil {
			if result.ctx.Err() != context.Canceled || result.closed.Load() != 1 {
				t.Error("concurrently admitted flow escaped cancellation/close")
			}
		} else if !errors.Is(result.err, errFixedRouteUnavailable) || result.closed.Load() != 0 {
			t.Error("rejected flow has wrong admission result", result.err)
		}
		result.free()
	}
	gate.reset()
}

// This wraps the existing transport fixture solely to hold a query across a
// gate change. Its eventual success deliberately ignores the canceled context.
type delayedGateDNS struct {
	*fakePrivacyDNS
	started chan context.Context
	finish  chan struct{}
}

func (d *delayedGateDNS) Exchange(ctx context.Context, q *dns.Msg) (*dns.Msg, error) {
	d.started <- ctx
	<-d.finish
	return d.fakePrivacyDNS.Exchange(context.Background(), q)
}

func TestEncryptedPoolGateCancelsInflightRejectsNewAndDropsLateSuccess(t *testing.T) {
	for _, stage := range []string{"primary", "backup", "cooldown"} {
		t.Run(stage, func(t *testing.T) {
			gate := &routeGate{}
			gate.configure(map[string]bool{"a": true})
			delayed := &delayedGateDNS{fakePrivacyDNS: &fakePrivacyDNS{}, started: make(chan context.Context, 2), finish: make(chan struct{})}
			var finishOnce sync.Once
			finish := func() { finishOnce.Do(func() { close(delayed.finish) }) }
			t.Cleanup(finish)
			first, second := &fakePrivacyDNS{}, &fakePrivacyDNS{}
			var primary, backup adapter.DNSTransport = delayed, second
			if stage != "primary" {
				first.fail = true
				primary, backup = first, delayed
			}
			pool := &encryptedPool{gate: gate, outbound: "a", primary: primary, backup: backup, pool: "route"}
			if stage == "cooldown" {
				pool.primaryRetry = time.Now().Add(time.Minute)
			}
			q := new(dns.Msg)
			q.SetQuestion("gate-fixture.test.", dns.TypeA)
			type reply struct {
				answer *dns.Msg
				err    error
			}
			result := make(chan reply, 1)
			go func() {
				answer, err := pool.Exchange(context.Background(), q)
				result <- reply{answer: answer, err: err}
			}()
			var exchangeContext context.Context
			select {
			case exchangeContext = <-delayed.started:
			case <-time.After(time.Second):
				t.Fatal("DNS query did not reach its frozen route transport")
			}
			before := pool.Environment()
			gate.setBlocked("a", true)
			blockedEnvironment := pool.Environment()
			if reflect.DeepEqual(before, blockedEnvironment) || exchangeContext.Err() != context.Canceled {
				t.Fatal("block left old DNS context/cache environment alive")
			}
			if answer, err := pool.Exchange(context.Background(), q); answer != nil || !errors.Is(err, errFixedRouteUnavailable) {
				t.Fatal("blocked DNS query reached a resolver", answer, err)
			}
			finish()
			select {
			case got := <-result:
				if got.answer != nil || got.err == nil {
					t.Fatal("late successful DNS reply crossed the block", got.answer, got.err)
				}
			case <-time.After(time.Second):
				t.Fatal("canceled DNS request did not drain")
			}
			if delayed.calls != 1 || second.calls != 0 {
				t.Fatal("blocked request contacted another transport", delayed.calls, second.calls)
			}
			gate.setBlocked("a", false)
			if reflect.DeepEqual(pool.Environment(), blockedEnvironment) || reflect.DeepEqual(pool.Environment(), before) {
				t.Fatal("recovery reused an old DNS cache environment")
			}
			if answer, err := pool.Exchange(context.Background(), q); answer == nil || err != nil || delayed.calls != 2 {
				t.Fatal("DNS did not resume on the same recovered route", answer, err, delayed.calls)
			}
		})
	}
}
