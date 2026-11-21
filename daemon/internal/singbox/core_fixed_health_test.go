package singbox

import (
	"context"
	"crypto/x509"
	"errors"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
)

func fixedHealthFixture(t *testing.T, results ...fixedHealthVerdict) *fixedHealth {
	t.Helper()
	h := newFixedHealth(context.Background())
	t.Cleanup(h.cancel)
	replies := make(chan fixedHealthVerdict, len(results))
	for _, verdict := range results {
		replies <- verdict
	}
	h.probe = func(ctx context.Context, _ adapter.Outbound) fixedHealthVerdict {
		select {
		case result := <-replies:
			return result
		case <-ctx.Done():
			return fixedHealthInconclusive
		}
	}
	return h
}

func fixedHealthTestOutbound(string) (adapter.Outbound, bool) { return nil, true }

func finishFixedHealthRound(t *testing.T, h *fixedHealth, now time.Time, version uint64, candidates map[string]bool) (string, bool, bool) {
	t.Helper()
	h.poll(now, version, candidates, fixedHealthTestOutbound)
	round := h.active
	if round == nil {
		t.Fatal("no health round was started")
	}
	return consumeFixedHealthRound(t, h, now, version, candidates, round)
}

func consumeFixedHealthRound(t *testing.T, h *fixedHealth, now time.Time, version uint64, candidates map[string]bool, round *fixedHealthRound) (string, bool, bool) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		tag, blocked, changed := h.poll(now, version, candidates, fixedHealthTestOutbound)
		if changed || h.active != round {
			return tag, blocked, changed
		}
		select {
		case <-deadline.C:
			t.Fatal("health round did not finish")
		default:
			runtime.Gosched()
		}
	}
}

func TestFixedHealthRequiresRepeatedFailureAndRecovery(t *testing.T) {
	h := fixedHealthFixture(t, fixedHealthFailed, fixedHealthFailed, fixedHealthPassed, fixedHealthPassed)
	now := time.Unix(100, 0)
	candidates := map[string]bool{"a": false}
	if _, _, changed := finishFixedHealthRound(t, h, now, 1, candidates); changed {
		t.Fatal("one failed round opened the breaker")
	}
	if h.active != nil {
		t.Fatal("completed round remains active")
	}
	h.poll(now.Add(fixedHealthInterval-time.Millisecond), 1, candidates, fixedHealthTestOutbound)
	if h.active != nil {
		t.Fatal("health round ignored its interval")
	}
	tag, blocked, changed := finishFixedHealthRound(t, h, now.Add(fixedHealthInterval), 1, candidates)
	if !changed || tag != "a" || !blocked {
		t.Fatal("repeated failures did not block a", tag, blocked, changed)
	}
	candidates["a"] = true
	if _, _, changed := finishFixedHealthRound(t, h, now.Add(2*fixedHealthInterval), 1, candidates); changed {
		t.Fatal("one successful round reopened a")
	}
	tag, blocked, changed = finishFixedHealthRound(t, h, now.Add(3*fixedHealthInterval), 1, candidates)
	if !changed || tag != "a" || blocked {
		t.Fatal("repeated recovery did not reopen a", tag, blocked, changed)
	}
}

func TestFixedHealthInconclusiveInterruptsFailureSequence(t *testing.T) {
	h := fixedHealthFixture(t, fixedHealthFailed, fixedHealthInconclusive, fixedHealthFailed)
	for i := 0; i < 3; i++ {
		if _, _, changed := finishFixedHealthRound(t, h, time.Unix(100, 0).Add(time.Duration(i)*fixedHealthInterval), 1, map[string]bool{"a": false}); changed {
			t.Fatal("nonconsecutive failures opened breaker", i)
		}
	}
}

func TestFixedHealthInvalidationDropsOldResultAndFailureCount(t *testing.T) {
	for _, nextVersion := range []uint64{1, 2} {
		t.Run(string(rune('0'+nextVersion)), func(t *testing.T) {
			h := fixedHealthFixture(t, fixedHealthFailed, fixedHealthFailed, fixedHealthFailed)
			now := time.Unix(100, 0)
			candidates := map[string]bool{"a": false}
			finishFixedHealthRound(t, h, now, 1, candidates)
			h.poll(now.Add(fixedHealthInterval), 1, candidates, fixedHealthTestOutbound)
			old := h.active
			if old == nil {
				t.Fatal("expected in-flight probe")
			}
			h.invalidate(nextVersion)
			// Drain the canceled worker without scheduling a replacement yet.
			if _, _, changed := consumeFixedHealthRound(t, h, now, nextVersion, nil, old); changed {
				t.Fatal("old result committed after invalidation")
			}
			if _, _, changed := finishFixedHealthRound(t, h, now.Add(2*fixedHealthInterval), nextVersion, candidates); changed {
				t.Fatal("failure count survived invalidation")
			}
		})
	}
}

func TestFixedHealthCancelDrainsWorker(t *testing.T) {
	h := newFixedHealth(context.Background())
	started, stopped := make(chan struct{}), make(chan struct{})
	h.probe = func(ctx context.Context, _ adapter.Outbound) fixedHealthVerdict {
		close(started)
		<-ctx.Done()
		close(stopped)
		return fixedHealthFailed
	}
	h.poll(time.Unix(100, 0), 1, map[string]bool{"a": false}, fixedHealthTestOutbound)
	<-started
	h.cancel()
	select {
	case <-stopped:
	default:
		t.Fatal("cancel returned before worker drained")
	}
	h.poll(time.Unix(200, 0), 1, map[string]bool{"a": false}, fixedHealthTestOutbound)
	if h.active != nil {
		t.Fatal("closed scheduler started new probe")
	}
}

func TestFixedHealthDoesNotMixRoutesOrRunParallelRounds(t *testing.T) {
	h := fixedHealthFixture(t, fixedHealthFailed, fixedHealthFailed)
	now := time.Unix(100, 0)
	candidates := map[string]bool{"a": false, "b": false}
	h.poll(now, 1, candidates, fixedHealthTestOutbound)
	first := h.active
	if first == nil || first.tag != "a" {
		t.Fatal("first route missing")
	}
	if _, _, changed := consumeFixedHealthRound(t, h, now, 1, candidates, first); changed {
		t.Fatal("first failure changed gate")
	}
	second := h.active
	if second == nil || second.tag != "b" {
		t.Fatal("other protected route was starved")
	}
	if _, _, changed := consumeFixedHealthRound(t, h, now, 1, candidates, second); changed {
		t.Fatal("different route failures combined")
	}
}

func TestFixedHealthSingleTargetFailureCannotBlock(t *testing.T) {
	for _, fixture := range []struct {
		name       string
		a, b, want fixedHealthVerdict
	}{
		{"one succeeds", fixedHealthPassed, fixedHealthFailed, fixedHealthPassed},
		{"one inconclusive", fixedHealthInconclusive, fixedHealthFailed, fixedHealthInconclusive},
		{"both fail", fixedHealthFailed, fixedHealthFailed, fixedHealthFailed},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			got := runFixedHealthTargets(context.Background(), []string{"a", "b"}, func(_ context.Context, target string) fixedHealthVerdict {
				if target == "a" {
					return fixture.a
				}
				return fixture.b
			})
			if got != fixture.want {
				t.Fatal("target verdict aggregation", got, fixture.want)
			}
		})
	}
}

func TestFixedHealthSuccessCancelsAndDrainsSibling(t *testing.T) {
	siblingStarted, siblingStopped := make(chan struct{}), make(chan struct{})
	got := runFixedHealthTargets(context.Background(), []string{"passing", "hanging"}, func(ctx context.Context, target string) fixedHealthVerdict {
		if target == "passing" {
			<-siblingStarted
			return fixedHealthPassed
		}
		close(siblingStarted)
		<-ctx.Done()
		close(siblingStopped)
		return fixedHealthInconclusive
	})
	if got != fixedHealthPassed {
		t.Fatal("passing target was lost")
	}
	select {
	case <-siblingStopped:
	default:
		t.Fatal("probe returned before its canceled sibling")
	}
}

func TestFixedHealthTargetErrorsDoNotBlameNode(t *testing.T) {
	certificate := x509.UnknownAuthorityError{}
	for _, fixture := range []struct {
		name string
		err  error
		want fixedHealthVerdict
	}{
		{"target certificate", certificate, fixedHealthInconclusive},
		{"target protocol", errors.New("malformed HTTP response"), fixedHealthInconclusive},
		{"node certificate", nodeMeasurementFailure(certificate), fixedHealthFailed},
		{"node refused", nodeMeasurementFailure(syscall.ECONNREFUSED), fixedHealthFailed},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			if got := fixedHealthFailure(fixture.err); got != fixture.want {
				t.Fatal("wrong failure scope", got, fixture.want)
			}
		})
	}
}
