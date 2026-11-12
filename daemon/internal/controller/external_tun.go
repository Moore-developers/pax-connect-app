package controller

import (
	"context"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/network"
)

// ExternalTUNAssess reports whether an interface other than own (Pax's utun, "" when Pax is not
// running) carries the public default routes.
type ExternalTUNAssess func(ctx context.Context, own string) network.ConflictResult

// WithExternalTUN makes the physical observer also watch for a non-Pax utun taking the public
// routes (for example v2rayN). It runs on the observer's own triggers (route events, startup and
// the 60s fallback), never on the 2s local sample tick, and only a changed result is committed.
func WithExternalTUN(assess ExternalTUNAssess) PhysicalOption {
	return func(o *physicalOptions) { o.externalTUN = assess }
}

// startExternalTUN runs assessments off the observer loop so a slow route lookup cannot delay
// local samples. Triggers coalesce; the returned stop waits for the worker.
func (c *Controller) startExternalTUN(parent context.Context, assess ExternalTUNAssess) (trigger func(), stop func()) {
	ctx, cancel := context.WithCancel(parent)
	wake := make(chan struct{}, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case <-wake:
				c.refreshExternalTUN(ctx, assess)
			}
		}
	}()
	return func() {
			select {
			case wake <- struct{}{}:
			default:
			}
		}, func() {
			cancel()
			wg.Wait()
		}
}

func (c *Controller) refreshExternalTUN(ctx context.Context, assess ExternalTUNAssess) {
	bounded, done := context.WithTimeout(ctx, 5*time.Second)
	defer done()
	own, known := c.paxInterface(bounded)
	if !known {
		return // Pax may own a utun we cannot name yet; keep the previous answer.
	}
	result := assess(bounded, own)
	var next *paxv1.ExternalTUN
	switch result.Kind {
	case network.ConflictOtherUTUN:
		next = &paxv1.ExternalTUN{Interface: result.Interface, Target: result.Target, At: timestamppb.Now()}
	case network.ConflictObservationError, network.ConflictUnknown:
		return // No evidence either way: neither claim nor clear.
	}
	if bounded.Err() != nil {
		return
	}
	c.setExternalTUN(next)
}

// paxInterface names Pax's own utun. known is false when protection is active but the core
// cannot say which interface it owns, so Pax's utun is never reported as external.
func (c *Controller) paxInterface(ctx context.Context) (own string, known bool) {
	if observer, ok := c.coreSession.(coreObserver); ok {
		observed, cancel := context.WithTimeout(ctx, time.Second)
		value, err := observer.ObserveCore(observed)
		cancel()
		if err == nil && value.Status == "STARTED" {
			own = value.Interface
		}
	}
	if own != "" {
		return own, true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	status := c.current.Status
	return "", status == paxv1.ProtectionStatus_PROTECTION_STATUS_OFF || status == paxv1.ProtectionStatus_PROTECTION_STATUS_UNSPECIFIED
}

// setExternalTUN commits only when the interface/target pair appears, changes or disappears;
// At stays the first time the current pair was seen.
func (c *Controller) setExternalTUN(next *paxv1.ExternalTUN) {
	c.mu.Lock()
	defer c.mu.Unlock()
	current := c.current.ExternalTun
	if current == nil && next == nil || current != nil && next != nil && current.Interface == next.Interface && current.Target == next.Target {
		return
	}
	c.current.ExternalTun = next
	c.commitLocked()
}
