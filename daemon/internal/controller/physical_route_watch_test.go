package controller

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"pax-connect/daemon/internal/network"
)

type routeFeed struct {
	messages chan error
	once     sync.Once
}

func (f *routeFeed) Next() error {
	err, ok := <-f.messages
	if !ok {
		return errors.New("closed")
	}
	return err
}
func (f *routeFeed) Close() error { f.once.Do(func() { close(f.messages) }); return nil }

type physicalFixture struct {
	c       *Controller
	feed    *routeFeed
	samples atomic.Int32
	epoch   atomic.Value
}

// newPhysicalFixture starts the observer on a fake PF_ROUTE source; openErr simulates a
// subscription that cannot be opened.
func newPhysicalFixture(t *testing.T, openErr error) *physicalFixture {
	t.Helper()
	f := &physicalFixture{c: newTestController(t, &fakeTester{}), feed: &routeFeed{messages: make(chan error, 16)}}
	f.epoch.Store("A")
	f.c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		f.samples.Add(1)
		return network.Physical{Name: "en0", Index: 1, Epoch: f.epoch.Load().(string)}, nil
	}, func(context.Context, network.Physical) (string, string, string) { return "1.1.1.1", "", "" },
		WithRouteWatch(func(ctx context.Context) *network.RouteWatch {
			return network.WatchRoutes(ctx, func() (network.RouteMessageSource, error) {
				if openErr != nil {
					return nil, openErr
				}
				return f.feed, nil
			}, network.RouteWatchOptions{})
		}))
	return f
}

func (f *physicalFixture) stop(t *testing.T) {
	t.Helper()
	if err := f.c.CloseProxy(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPhysicalObserverIdleSamplesOnlyAtFallbackFrequencyYetStaysFresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newPhysicalFixture(t, nil)
		defer f.stop(t)
		for range 5 * 60 {
			time.Sleep(time.Second)
			sample, _ := f.c.physical.localSnapshot()
			if !sample.valid || time.Since(sample.at) > pathSampleFreshness {
				t.Fatal("downstream freshness contract broken while idle", time.Since(sample.at))
			}
		}
		// startup + one fallback per minute, not one per 2s (150).
		if got := f.samples.Load(); got < 5 || got > 7 {
			t.Fatal("idle sampling is not at fallback frequency", got)
		}
	})
}

func TestPhysicalObserverSamplesOnRouteEventAndPublishesNewEpoch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newPhysicalFixture(t, nil)
		defer f.stop(t)
		time.Sleep(5 * time.Second)
		before := f.samples.Load()
		f.epoch.Store("B")
		f.feed.messages <- nil
		time.Sleep(time.Second)
		// The event sample, plus at most the verification sample after the exit-IP probe.
		if got := f.samples.Load(); got < before+1 || got > before+2 {
			t.Fatal("route event did not trigger a sample", before, got)
		}
		if sample, _ := f.c.physical.localSnapshot(); sample.epoch != "B" {
			t.Fatal("new epoch not published within 1s of the event", sample.epoch)
		}
	})
}

func TestPhysicalObserverFallsBackToPollingWhenSubscriptionFails(t *testing.T) {
	for _, scenario := range []string{"open_fails", "lost_later"} {
		t.Run(scenario, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var openErr error
				if scenario == "open_fails" {
					openErr = errors.New("route socket denied")
				}
				f := newPhysicalFixture(t, openErr)
				defer f.stop(t)
				time.Sleep(10 * time.Second)
				if scenario == "lost_later" {
					if got := f.samples.Load(); got > 2 {
						t.Fatal("healthy subscription polled", got)
					}
					f.feed.messages <- errors.New("read failed")
					time.Sleep(10 * time.Second)
					if got := f.samples.Load(); got < 4 {
						t.Fatal("lost subscription did not resume polling", got)
					}
					return
				}
				if got := f.samples.Load(); got < 5 {
					t.Fatal("unavailable subscription did not poll", got)
				}
				f.epoch.Store("B")
				time.Sleep(3 * time.Second)
				if sample, _ := f.c.physical.localSnapshot(); sample.epoch != "B" {
					t.Fatal("polling fallback missed an epoch change", sample.epoch)
				}
			})
		})
	}
}

func TestWaitEpochChangeFollowsObserverWithoutOwnSampling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newPhysicalFixture(t, nil)
		defer f.stop(t)
		time.Sleep(5 * time.Second)
		start := time.Now()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		changed := make(chan bool, 1)
		go func() { changed <- f.c.physical.waitEpochChange(ctx, "A", start) }()
		quiet := f.samples.Load()
		time.Sleep(30 * time.Second)
		select {
		case <-changed:
			t.Fatal("unchanged epoch ended the wait")
		default:
		}
		// A private 2s sampler would add ~15 samples here; only the shared fallback may run.
		if got := f.samples.Load(); got > quiet+1 {
			t.Fatal("watcher ran its own samples", quiet, got)
		}
		f.epoch.Store("B")
		f.feed.messages <- nil
		time.Sleep(time.Second)
		select {
		case ok := <-changed:
			if !ok {
				t.Fatal("wait ended without an epoch change")
			}
		default:
			t.Fatal("epoch change was not observed within 1s")
		}
		cancel()
		if f.c.physical.waitEpochChange(ctx, "B", time.Now()) {
			t.Fatal("cancelled wait reported a change")
		}
	})
}
