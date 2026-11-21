//go:build integration

package singbox

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
)

func fixedLifecycleProfile(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf(`{"log":{"disabled":true},"experimental":{"cache_file":{"path":%q}},"outbounds":[{"type":"direct","tag":"A"},{"type":"direct","tag":"B"},{"type":"direct","tag":"direct"}],"route":{"final":"direct","rules":[{"domain":"a.lifecycle.test","outbound":"A"}]}}`, filepath.Join(t.TempDir(), "cache.db"))
}

func fixedLifecycleRuntime(t *testing.T) (*CoreRuntime, string) {
	t.Helper()
	r := NewCoreRuntime(context.Background())
	t.Cleanup(r.Close)
	t.Cleanup(func() {
		if err := r.Clear(); err != nil {
			t.Error(err)
		}
	})
	profile := fixedLifecycleProfile(t)
	if err := r.Apply(profile); err != nil {
		t.Fatal(err)
	}
	return r, profile
}

func TestFixedLifecycleApplyAndClearDrainOldOutboundProbe(t *testing.T) {
	for _, action := range []string{"apply", "clear"} {
		t.Run(action, func(t *testing.T) {
			r, profile := fixedLifecycleRuntime(t)
			original := r.current().Instance()
			out, found := original.Box().Outbound().Outbound("A")
			if !found {
				t.Fatal("original fixed outbound is missing")
			}
			started := make(chan adapter.Outbound, 1)
			canceled, exited := make(chan struct{}), make(chan struct{})
			allowExit := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(allowExit) }) }
			t.Cleanup(unblock)
			r.fixedHealth.probe = func(ctx context.Context, actual adapter.Outbound) fixedHealthVerdict {
				defer close(exited)
				started <- actual
				<-ctx.Done()
				close(canceled)
				<-allowExit
				// Keep the old outbound live until the worker has relinquished it.
				_ = actual.Tag()
				return fixedHealthPassed
			}
			r.fixedHealth.poll(time.Now(), r.pathVersion.Load(), map[string]bool{"A": false}, original.Box().Outbound().Outbound)
			select {
			case actual := <-started:
				if actual != out {
					t.Fatal("probe used a different outbound instance")
				}
			case <-time.After(time.Second):
				t.Fatal("old-instance probe did not start")
			}
			result := make(chan error, 1)
			go func() {
				if action == "apply" {
					result <- r.Apply(profile)
				} else {
					result <- r.Clear()
				}
			}()
			t.Cleanup(func() { unblock(); <-result })
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("lifecycle change did not cancel the old probe")
			}
			if r.current().Instance() != original {
				t.Fatal("lifecycle disposed the outbound before its probe exited")
			}
			select {
			case err := <-result:
				result <- err // Keep the join cleanup independent of assertion failures.
				t.Fatal("lifecycle returned while its old-outbound worker was live", err)
			default:
			}
			unblock()
			select {
			case err := <-result:
				result <- err
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("lifecycle did not finish after its worker exited")
			}
			select {
			case <-exited:
			default:
				t.Fatal("lifecycle returned before old probe completion")
			}
			if r.fixedHealth.closed || r.fixedHealth.ctx.Err() != nil {
				t.Fatal("instance lifecycle canceled the reusable health owner")
			}
			if action == "clear" {
				if r.current().Instance() != nil || r.fixedHealth.active != nil {
					t.Fatal("Clear retained an old instance or health worker")
				}
				if err := r.Apply(profile); err != nil {
					t.Fatal(err)
				}
			}
			// A subsequent instance must still be able to start a health round.
			restarted := make(chan struct{}, 1)
			r.fixedHealth.probe = func(ctx context.Context, _ adapter.Outbound) fixedHealthVerdict {
				restarted <- struct{}{}
				<-ctx.Done()
				return fixedHealthInconclusive
			}
			r.fixedHealth.poll(time.Now(), r.pathVersion.Load(), map[string]bool{"A": false}, r.current().Instance().Box().Outbound().Outbound)
			select {
			case <-restarted:
			case <-time.After(time.Second):
				t.Fatal("new instance could not reuse the health owner")
			}
			r.fixedHealth.drain()
		})
	}
}

func TestFixedLifecycleMaintainInvalidatesOldNetworkResultBeforeConsumption(t *testing.T) {
	r, _ := fixedLifecycleRuntime(t)
	instance := r.current().Instance()
	if monitor := instance.Box().Network().InterfaceMonitor(); monitor != nil {
		current := monitor.DefaultInterface()
		if current == nil || current.Name == "lo0" || strings.HasPrefix(current.Name, "utun") {
			t.Skip("no physical interface is available for the runtime observation path")
		}
	}
	_, release, err := r.fixed.gate.acquire(context.Background(), "A", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	r.fixedHealth.probe = func(context.Context, adapter.Outbound) fixedHealthVerdict { return fixedHealthFailed }
	r.fixedHealth.poll(time.Now(), r.pathVersion.Load(), map[string]bool{"A": false}, instance.Box().Outbound().Outbound)
	r.fixedHealth.counts["A"].failures = 1
	deadline := time.Now().Add(time.Second)
	for len(r.fixedHealth.active.done) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("old-network result did not become ready")
		}
		runtime.Gosched()
	}
	generation := r.fixedHealth.generation
	r.fixedNetwork = ^instance.Box().Network().NetworkEnvironment()
	r.Maintain(context.Background())
	if r.fixedHealth.generation == generation || r.fixed.blocked["A"] {
		t.Fatal("old-network failure was consumed before physical environment invalidation")
	}
	r.fixedHealth.drain()
}

type fixedLifecycleTracker struct {
	adapter.ConnectionTracker
	gate   *routeGate
	cancel context.CancelFunc
	tag    string
	leases int
}

func (t *fixedLifecycleTracker) RoutedConnection(_ context.Context, conn net.Conn, _ adapter.InboundContext, _ adapter.Rule, outbound adapter.Outbound) net.Conn {
	t.tag, t.leases = outbound.Tag(), t.gate.pending(outbound.Tag())
	t.cancel() // Stop before Direct's dial; the test never needs a network peer.
	return conn
}

func TestFixedLifecycleFirstConnectionAfterRoutingUpdateRetainsNewFixedLease(t *testing.T) {
	r, profile := fixedLifecycleRuntime(t)
	next := strings.Replace(profile, `"a.lifecycle.test","outbound":"A"`, `"b.lifecycle.test","outbound":"B"`, 1)
	r.SetRoutingSources([]string{`{"Package":"Lifecycle","Method":"domain","Target":"b.lifecycle.test","FixedRouteName":"B"}`})
	if err := r.UpdateRouting(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(r.ctx)
	defer cancel()
	tracker := &fixedLifecycleTracker{gate: r.fixed.gate, cancel: cancel}
	router := r.current().Instance().Box().Router()
	router.AppendTracker(tracker)
	inbound, peer := net.Pipe()
	defer inbound.Close()
	defer peer.Close()
	closed := make(chan error, 1)
	router.RouteConnectionEx(ctx, inbound, adapter.InboundContext{
		InboundType: "mixed", Destination: M.ParseSocksaddr("b.lifecycle.test:443"),
		DestinationAddresses: []netip.Addr{netip.MustParseAddr("192.0.2.1")},
	}, func(err error) { closed <- err })
	if tracker.tag != "B" || tracker.leases != 1 {
		t.Fatal("first matched fixed B connection missed admission tracking", tracker.tag, tracker.leases)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("canceled fixture connection did not close")
	}
	if r.fixed.gate.pending("B") != 0 {
		t.Fatal("canceled first connection retained its fixed lease")
	}
}

type fixedLifecycleDialOutbound struct {
	adapter.Outbound
	started, canceled, exited chan struct{}
	allowExit                 <-chan struct{}
}

func (o *fixedLifecycleDialOutbound) DialContext(ctx context.Context, _ string, _ M.Socksaddr) (net.Conn, error) {
	defer close(o.exited)
	close(o.started)
	<-ctx.Done()
	close(o.canceled)
	<-o.allowExit
	return nil, ctx.Err()
}

func TestFixedLifecycleProbeJoinsDetachedHTTPDialOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	allowExit := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(allowExit) }) }
	outbound := &fixedLifecycleDialOutbound{
		started: make(chan struct{}), canceled: make(chan struct{}), exited: make(chan struct{}), allowExit: allowExit,
	}
	result := make(chan fixedHealthVerdict, 1)
	probeExited := make(chan struct{})
	go func() {
		defer close(probeExited)
		result <- probeFixedTarget(ctx, outbound, "https://not-a-network-peer.invalid/generate_204", nil)
	}()
	t.Cleanup(func() {
		cancel()
		unblock()
		select {
		case <-probeExited:
		case <-time.After(time.Second):
			t.Error("HTTP probe did not drain after its fake outbound was released")
		}
	})
	select {
	case <-outbound.started:
	case <-time.After(time.Second):
		t.Fatal("HTTP transport did not invoke the real outbound boundary")
	}
	cancel()
	select {
	case <-outbound.canceled:
	case <-time.After(time.Second):
		t.Fatal("round cancellation did not reach the detached HTTP dial")
	}
	// client.Do can return on cancellation while its detached dial is still
	// running. Keep that dial live and verify the probe joins it before return.
	select {
	case <-probeExited:
		t.Fatal("probe returned while its detached outbound dial remained live")
	case <-time.After(25 * time.Millisecond):
	}
	unblock()
	select {
	case got := <-result:
		if got != fixedHealthInconclusive {
			t.Fatal("canceled round produced a usable health verdict", got)
		}
	case <-time.After(time.Second):
		t.Fatal("probe did not finish after its outbound dial exited")
	}
	select {
	case <-outbound.exited:
	default:
		t.Fatal("probe return preceded its outbound dial's exit")
	}
}
