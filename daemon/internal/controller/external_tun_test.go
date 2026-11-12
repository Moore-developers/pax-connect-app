package controller

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/network"
)

type externalTUNFixture struct {
	*physicalFixture
	mu     sync.Mutex
	result network.ConflictResult
	calls  int
}

func (f *externalTUNFixture) set(r network.ConflictResult) {
	f.mu.Lock()
	f.result = r
	f.mu.Unlock()
}
func (f *externalTUNFixture) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newExternalTUNFixture(t *testing.T) *externalTUNFixture {
	t.Helper()
	f := &externalTUNFixture{physicalFixture: &physicalFixture{c: newTestController(t, &fakeTester{}), feed: &routeFeed{messages: make(chan error, 16)}}}
	f.epoch.Store("A")
	f.c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		return network.Physical{Name: "en0", Index: 1, Epoch: "A"}, nil
	}, func(context.Context, network.Physical) (string, string, string) { return "1.1.1.1", "", "" },
		WithRouteWatch(func(ctx context.Context) *network.RouteWatch {
			return network.WatchRoutes(ctx, func() (network.RouteMessageSource, error) { return f.feed, nil }, network.RouteWatchOptions{})
		}),
		WithExternalTUN(func(_ context.Context, own string) network.ConflictResult {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.calls++
			return f.result
		}))
	return f
}

func (f *externalTUNFixture) snapshot(t *testing.T) *paxv1.Snapshot {
	t.Helper()
	s, err := f.c.GetSnapshot(context.Background(), &paxv1.GetSnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestExternalTUNAppearsStaysQuietAndClears(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newExternalTUNFixture(t)
		defer f.stop(t)
		time.Sleep(5 * time.Second)
		if f.snapshot(t).ExternalTun != nil || f.count() == 0 {
			t.Fatal("startup must assess once and report nothing for a clean network", f.count())
		}
		// v2rayN-style takeover: appears on a route event.
		f.set(network.ConflictResult{Kind: network.ConflictOtherUTUN, Interface: "utun6", Target: "128.0.0.0/1"})
		f.feed.messages <- nil
		time.Sleep(time.Second)
		appeared := f.snapshot(t)
		if got := appeared.ExternalTun; got.GetInterface() != "utun6" || got.GetTarget() != "128.0.0.0/1" || got.At == nil {
			t.Fatal("external tun not published", got)
		}
		// Same result again (event and 60s fallback): no commit, version unchanged, At kept.
		f.feed.messages <- nil
		time.Sleep(2 * time.Minute)
		again := f.snapshot(t)
		if again.SnapshotVersion != appeared.SnapshotVersion || !again.ExternalTun.At.AsTime().Equal(appeared.ExternalTun.At.AsTime()) {
			t.Fatal("unchanged external tun was re-committed", appeared.SnapshotVersion, again.SnapshotVersion)
		}
		// Observation failure keeps the previous answer.
		f.set(network.ConflictResult{Kind: network.ConflictObservationError})
		f.feed.messages <- nil
		time.Sleep(time.Second)
		if f.snapshot(t).ExternalTun == nil {
			t.Fatal("observation error cleared the notice")
		}
		// Gone.
		f.set(network.ConflictResult{Kind: network.ConflictNone})
		f.feed.messages <- nil
		time.Sleep(time.Second)
		if f.snapshot(t).ExternalTun != nil {
			t.Fatal("external tun not cleared")
		}
	})
}

func TestExternalTUNIgnoresPaxOwnInterfaceAndSkipsLocalTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newExternalTUNFixture(t)
		defer f.stop(t)
		f.set(network.ConflictResult{Kind: network.ConflictPax, Interface: "utun9", Target: "0.0.0.0/1"})
		time.Sleep(5 * time.Minute)
		if f.snapshot(t).ExternalTun != nil {
			t.Fatal("Pax's own utun reported as external")
		}
		// startup + one fallback per minute; a 2s tick would be ~150.
		if got := f.count(); got < 5 || got > 7 {
			t.Fatal("assessment is not on observer triggers", got)
		}
	})
}
