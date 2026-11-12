package controller

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/network"
)

func TestCurrentPathWakeWaitsForFreshNetworkWithoutBlockingPower(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newTestController(t, &fakeTester{})
		route := seedLocalRoute(t, c)
		results, stop := operationSink(c)
		defer stop()
		selected, e := c.SelectRoute(context.Background(), &pb.SelectRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision})
		if e != nil {
			t.Fatal(e)
		}
		awaitResult(t, results, selected.OperationId)
		var available atomic.Bool
		available.Store(true)
		c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
			if !available.Load() {
				return network.Physical{}, context.DeadlineExceeded
			}
			return network.Physical{Name: "en0", Index: 1, Epoch: "network"}, nil
		}, func(context.Context, network.Physical) (string, string, string) { return "1.1.1.1", "", "" })
		time.Sleep(3 * time.Second)
		core := &lifecycleCore{}
		c.ConfigureUserMode(func() {}, 0)
		c.ConfigureCoreSession(core)
		on, e := c.SetProxyEnabled(context.Background(), &pb.SetProxyEnabledRequest{Enabled: true})
		if e != nil {
			t.Fatal(e)
		}
		awaitResult(t, results, on.OperationId)
		waitPathState(t, c, "passed")
		defer func() { _ = c.CloseProxy(context.Background()) }()
		if _, e = c.NotifyPowerEvent(context.Background(), &pb.PowerEventRequest{Sleeping: true, Sequence: 1}); e != nil {
			t.Fatal(e)
		}
		available.Store(false)
		before := time.Now()
		if _, e = c.NotifyPowerEvent(context.Background(), &pb.PowerEventRequest{Sequence: 2}); e != nil {
			t.Fatal(e)
		}
		if time.Since(before) >= time.Second {
			t.Fatal("power acknowledgement waited for network")
		}
		waitPathState(t, c, "waiting_network")
		time.Sleep(11 * time.Second)
		synctest.Wait()
		snapshot, _ := c.GetSnapshot(context.Background(), &pb.GetSnapshotRequest{})
		if snapshot.GetCurrentPathCheck().GetCause() != "physical_network_wait" {
			t.Fatalf("wait ended with %s", snapshot.GetCurrentPathCheck().GetCause())
		}
		core.pathMu.Lock()
		beforeCalls := len(core.requests)
		core.pathMu.Unlock()
		available.Store(true)
		time.Sleep(6 * time.Second)
		synctest.Wait()
		waitPathState(t, c, "passed")
		core.pathMu.Lock()
		afterCalls := len(core.requests)
		core.pathMu.Unlock()
		if afterCalls != beforeCalls+1 {
			t.Fatalf("network return checks=%d want one", afterCalls-beforeCalls)
		}
		time.Sleep(10 * time.Second)
		synctest.Wait()
		core.pathMu.Lock()
		final := len(core.requests)
		core.pathMu.Unlock()
		if final != afterCalls {
			t.Fatal("stable network repeated automatic check")
		}
	})
}

func wakeFixture(t *testing.T) (*Controller, *lifecycleCore, *atomic.Bool, <-chan *pb.OperationResult, func()) {
	t.Helper()
	c := newTestController(t, &fakeTester{})
	route := seedLocalRoute(t, c)
	results, stop := operationSink(c)
	selected, e := c.SelectRoute(context.Background(), &pb.SelectRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision})
	if e != nil {
		t.Fatal(e)
	}
	awaitResult(t, results, selected.OperationId)
	available := new(atomic.Bool)
	available.Store(true)
	c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		if !available.Load() {
			return network.Physical{}, context.DeadlineExceeded
		}
		return network.Physical{Name: "en0", Index: 1, Epoch: "network"}, nil
	}, func(context.Context, network.Physical) (string, string, string) { return "1.1.1.1", "", "" })
	time.Sleep(3 * time.Second)
	core := &lifecycleCore{}
	c.ConfigureUserMode(func() {}, 0)
	c.ConfigureCoreSession(core)
	on, e := c.SetProxyEnabled(context.Background(), &pb.SetProxyEnabledRequest{Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	awaitResult(t, results, on.OperationId)
	waitPathState(t, c, "passed")
	cleanup := func() { _ = c.CloseProxy(context.Background()); stop() }
	return c, core, available, results, cleanup
}

func requestCount(f *lifecycleCore) int {
	f.pathMu.Lock()
	defer f.pathMu.Unlock()
	return len(f.requests)
}

func TestCurrentPathWakeCancellationAndFollowupExpiry(t *testing.T) {
	for _, scenario := range []string{"off", "new_sleep", "expiry", "manual"} {
		t.Run(scenario, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c, core, available, results, cleanup := wakeFixture(t)
				defer cleanup()
				available.Store(false)
				_, _ = c.NotifyPowerEvent(context.Background(), &pb.PowerEventRequest{Sleeping: true, Sequence: 1})
				_, e := c.NotifyPowerEvent(context.Background(), &pb.PowerEventRequest{Sequence: 2})
				if e != nil {
					t.Fatal(e)
				}
				waitPathState(t, c, "waiting_network")
				baseline := requestCount(core)
				switch scenario {
				case "off":
					manual, e := c.RecheckCurrentPath(context.Background(), &pb.CurrentPathRecheckRequest{RequestId: "joined"})
					if e != nil {
						t.Fatal(e)
					}
					off, e := c.SetProxyEnabled(context.Background(), &pb.SetProxyEnabledRequest{})
					if e != nil {
						t.Fatal(e)
					}
					awaitResult(t, results, off.OperationId)
					// Cancellation is a distinct product outcome, never a new ON.
					snapshot, _ := c.GetSnapshot(context.Background(), &pb.GetSnapshotRequest{})
					if snapshot.Status != pb.ProtectionStatus_PROTECTION_STATUS_OFF {
						t.Fatal("OFF did not finish")
					}
					_ = manual
				case "new_sleep":
					_, e = c.NotifyPowerEvent(context.Background(), &pb.PowerEventRequest{Sleeping: true, Sequence: 3})
					if e != nil {
						t.Fatal(e)
					}
				default:
					time.Sleep(11 * time.Second)
					synctest.Wait()
					waitPathState(t, c, "inconclusive")
					if scenario == "expiry" {
						time.Sleep(time.Minute)
					} else {
						manual, e := c.RecheckCurrentPath(context.Background(), &pb.CurrentPathRecheckRequest{RequestId: "replace-followup"})
						if e != nil {
							t.Fatal(e)
						}
						awaitResult(t, results, manual.OperationId)
						baseline = requestCount(core)
					}
				}
				available.Store(true)
				time.Sleep(8 * time.Second)
				synctest.Wait()
				if requestCount(core) != baseline {
					t.Fatalf("%s revived a cancelled/expired check", scenario)
				}
			})
		})
	}
}

func TestCurrentPathWakeDuplicateAndManualShareFreshCheck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, core, _, results, cleanup := wakeFixture(t)
		defer cleanup()
		baseline := requestCount(core)
		_, _ = c.NotifyPowerEvent(context.Background(), &pb.PowerEventRequest{Sleeping: true, Sequence: 1})
		wake := &pb.PowerEventRequest{Sequence: 2}
		_, _ = c.NotifyPowerEvent(context.Background(), wake)
		waitPathState(t, c, "waiting_network")
		first, _ := c.GetSnapshot(context.Background(), &pb.GetSnapshotRequest{})
		_, _ = c.NotifyPowerEvent(context.Background(), wake)
		manual, e := c.RecheckCurrentPath(context.Background(), &pb.CurrentPathRecheckRequest{RequestId: "joined"})
		if e != nil {
			t.Fatal(e)
		}
		joined, _ := c.GetSnapshot(context.Background(), &pb.GetSnapshotRequest{})
		if first.GetCurrentPathCheck().GetCheckId() != joined.GetCurrentPathCheck().GetCheckId() {
			t.Fatal("duplicate wake restarted wait")
		}
		time.Sleep(6 * time.Second)
		synctest.Wait()
		if r := awaitResult(t, results, manual.OperationId); !r.Ok {
			t.Fatal(r)
		}
		if requestCount(core) != baseline+1 {
			t.Fatal("manual/wake sent duplicate HTTPS")
		}
		if _, e = c.RecheckCurrentPath(context.Background(), &pb.CurrentPathRecheckRequest{RequestId: "joined"}); e != nil {
			t.Fatal("same request did not return its ACK", e)
		}
	})
}

func TestCurrentPathWakeRequiresTwoSecondsOfPostWakeSamples(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, core, _, _, cleanup := wakeFixture(t)
		defer cleanup()
		before := requestCount(core)
		_, _ = c.NotifyPowerEvent(context.Background(), &pb.PowerEventRequest{Sleeping: true, Sequence: 1})
		_, _ = c.NotifyPowerEvent(context.Background(), &pb.PowerEventRequest{Sequence: 2})
		value := network.Physical{Name: "en0", Index: 1, Epoch: "network"}
		time.Sleep(100 * time.Millisecond)
		c.physical.publishLocalSample(value, nil)
		time.Sleep(100 * time.Millisecond)
		c.physical.publishLocalSample(value, nil)
		synctest.Wait()
		if requestCount(core) != before {
			t.Fatal("old stable epoch let two rapid samples bypass post-wake wait")
		}
		time.Sleep(3 * time.Second)
		synctest.Wait()
		waitPathState(t, c, "passed")
		if requestCount(core) != before+1 {
			t.Fatal("post-wake samples did not release exactly one check")
		}
	})
}

func TestCurrentPathSleepingNetworkEventsKeepSleepGateVisible(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _, available, _, cleanup := wakeFixture(t)
		defer cleanup()
		_, e := c.NotifyPowerEvent(context.Background(), &pb.PowerEventRequest{Sleeping: true, Sequence: 1})
		if e != nil {
			t.Fatal(e)
		}
		available.Store(false)
		time.Sleep(4 * time.Second)
		synctest.Wait()
		snapshot, _ := c.GetSnapshot(context.Background(), &pb.GetSnapshotRequest{})
		if snapshot.GetCurrentPathCheck().GetCause() != "device_sleeping" {
			t.Fatal("network sample erased sleep state")
		}
		if _, e = c.RecheckCurrentPath(context.Background(), &pb.CurrentPathRecheckRequest{RequestId: "sleep-network"}); e == nil {
			t.Fatal("sleep network event allowed a check")
		}
	})
}

type failingSleepCore struct{ lifecycleCore }

func (*failingSleepCore) Power(_ context.Context, sleeping bool, _ uint64) error {
	if sleeping {
		return context.DeadlineExceeded
	}
	return nil
}

func TestCurrentPathFailedSleepDeliveryStillPreventsManualCheck(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	route := seedLocalRoute(t, c)
	results, stop := operationSink(c)
	defer stop()
	selected, e := c.SelectRoute(context.Background(), &pb.SelectRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision})
	if e != nil {
		t.Fatal(e)
	}
	awaitResult(t, results, selected.OperationId)
	core := &failingSleepCore{}
	c.ConfigureUserMode(func() {}, 0)
	c.ConfigureCoreSession(core)
	c.ConfigureNetworkEpoch(func() (string, error) { return "network", nil })
	on, e := c.SetProxyEnabled(context.Background(), &pb.SetProxyEnabledRequest{Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	awaitResult(t, results, on.OperationId)
	waitPathState(t, c, "passed")
	defer func() { _ = c.CloseProxy(context.Background()) }()
	baseline := requestCount(&core.lifecycleCore)
	if _, e = c.NotifyPowerEvent(context.Background(), &pb.PowerEventRequest{Sleeping: true, Sequence: 1}); e == nil {
		t.Fatal("sleep failure hidden")
	}
	snapshot, _ := c.GetSnapshot(context.Background(), &pb.GetSnapshotRequest{})
	if snapshot.GetCurrentPathCheck().GetCause() != "device_sleeping" {
		t.Fatal("failed sleep lost sleeping presentation")
	}
	if _, e = c.RecheckCurrentPath(context.Background(), &pb.CurrentPathRecheckRequest{RequestId: "while-sleeping"}); e == nil {
		t.Fatal("failed sleep delivery allowed a new check")
	}
	if requestCount(&core.lifecycleCore) != baseline {
		t.Fatal("manual HTTPS ran while sleep was requested")
	}
}

// The fake is a public core boundary; never assert a private owner field.
var _ interface {
	VerifyCurrentPath(context.Context, config.CurrentPathRequest) (config.CurrentPathResult, error)
} = (*lifecycleCore)(nil)
