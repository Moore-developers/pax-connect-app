package controller

import (
	"context"
	"errors"
	"time"

	"pax-connect/daemon/internal/cadence"
	"pax-connect/daemon/internal/config"
)

// The background latency sampler for the current route runs every cadence.RouteAutoLatency
// (decisions/route-auto-latency.md). One 120-sample window covers about an hour.

// Round verdicts, reported to the optional observer; only "recorded" writes history.
const (
	autoProxyOff     = "proxy_off"
	autoSleeping     = "sleeping"
	autoNetworkDown  = "network_down"
	autoManualTest   = "manual_test"
	autoRouteMissing = "route_unavailable"
	autoBusy         = "busy"
	autoRecorded     = "recorded"
	autoDropped      = "dropped"
	autoStopped      = "stopped"
)

// autoLatencyTarget freezes what one round measures: the running binding plus the resolved record
// and connection version it started from. A sample is only recorded if all of it still holds.
type autoLatencyTarget struct {
	binding runningBinding
	record  config.RouteRecord
	view    config.TLSPolicyView
	version uint64
	limit   int
}

// autoLatencyLoop runs inside the route tester's lifetime and joins its worker group, so
// daemon shutdown waits for it; ticks come from the caller (nil: the real cadence timer).
func (c *Controller) autoLatencyLoop(owner *testOwner, ticks <-chan time.Time, tester RouteTester) {
	defer owner.workers.Done()
	loop := cadence.Loop{
		Name:     "route_auto_latency",
		Interval: func() time.Duration { return cadence.RouteAutoLatency },
		Run: func(context.Context) (bool, error) {
			verdict := c.autoLatencyRound(owner, tester)
			if c.autoLatencyObserve != nil {
				c.autoLatencyObserve(verdict)
			}
			return verdict == autoRecorded, nil
		},
	}
	if ticks != nil {
		loop.After = func(time.Duration) (<-chan time.Time, func()) { return ticks, func() {} }
	}
	loop.Serve(owner.lifetime)
}

// autoLatencyGate runs in the single owner. It names the current route when a round may measure,
// otherwise the reason to skip. Sleeping, offline and manual-test state are read, never changed.
func (c *Controller) autoLatencyGate(ctx context.Context) (autoLatencyTarget, string) {
	p := c.proxy
	if !p.desired.Load() || !p.running.valid() {
		return autoLatencyTarget{}, autoProxyOff
	}
	if p.sleeping {
		return autoLatencyTarget{}, autoSleeping
	}
	if c.physical != nil {
		if local, _ := c.physical.localSnapshot(); local.generation != 0 && !local.valid {
			return autoLatencyTarget{}, autoNetworkDown
		}
	}
	binding := p.running
	if _, testing := c.routes.testing[binding.route]; testing {
		return autoLatencyTarget{}, autoManualTest
	}
	current, ok := c.routes.catalog.Find(binding.route)
	if !ok {
		return autoLatencyTarget{}, autoRouteMissing
	}
	resolved, err := c.resolvedRecord(ctx, current)
	if err != nil || resolved.Config.TLSResolved.NeedsConfirmation {
		return autoLatencyTarget{}, autoRouteMissing
	}
	limit, exit, _ := c.measurementParallel()
	resolved.Config.MeasurementLatency, resolved.Config.MeasurementExit = limit, exit
	view := resolved.Config.TLSResolved
	return autoLatencyTarget{binding: binding, record: resolved, view: view, version: connectionVersion(resolved, view), limit: limit}, ""
}

// autoLatencyRound is one tick: gate in the owner, probe outside it, then re-gate and commit in
// the owner so a sample never outlives the route, version, power or network it was taken on.
func (c *Controller) autoLatencyRound(owner *testOwner, tester RouteTester) string {
	var target autoLatencyTarget
	var skip string
	if err := c.query(owner.lifetime, func(ctx context.Context) { target, skip = c.autoLatencyGate(ctx) }); err != nil {
		return autoStopped
	}
	if skip != "" {
		return skip
	}
	if !c.tryAcquireLatency(owner, target.limit) {
		return autoBusy // manual tests keep their admission; sampling never queues behind them
	}
	defer c.releaseLatency(owner)
	result, verdict := c.measureAutoLatency(owner.lifetime, target, tester)
	if verdict != "" {
		return verdict
	}
	committed := autoDropped
	if err := c.query(owner.lifetime, func(ctx context.Context) {
		again, skip := c.autoLatencyGate(ctx)
		if (skip != "" && skip != autoManualTest) || again.binding != target.binding || again.version != target.version {
			return
		}
		latency, _ := latencyHistory(target.binding.route, result)
		latency.Metric = config.MetricLatencyAuto
		c.appendHistory(ctx, latency)
		committed = autoRecorded
	}); err != nil {
		return autoStopped
	}
	return committed
}

// measureAutoLatency reuses the manual probe boundary and failure classification. A non-empty
// verdict means "no sample": the daemon stopped, or the physical network moved underneath it.
func (c *Controller) measureAutoLatency(lifetime context.Context, target autoLatencyTarget, tester RouteTester) (routeTestResult, string) {
	ctx, stop := context.WithTimeout(lifetime, testTimeout)
	defer stop()
	profile := target.record.Config
	profile.TLSResolved = target.view
	epoch := ""
	if c.physical != nil {
		native, err := c.physical.sample(ctx)
		if err != nil {
			return routeTestResult{}, autoNetworkDown
		}
		epoch = native.Epoch
		profile.MeasurementNetwork = epoch
		profile = bindRouteNetwork(profile, native)
	}
	if err := config.ValidateAddressFamily(profile); err != nil {
		return routeTestResult{}, autoNetworkDown
	}
	var (
		measured = config.RouteMeasurement{Method: "https204-dual-v2"}
		duration time.Duration
		err      error
	)
	if measurer, ok := tester.(RouteMeasurer); ok {
		measured, err = measurer.MeasureRoute(ctx, profile, func(config.RouteMeasurement) {})
		if measured.Method == "" {
			measured.Method = "https204-dual-v2"
		}
		duration = measured.Duration
	} else {
		duration, err = tester.TestRoute(ctx, profile)
	}
	if lifetime.Err() != nil {
		return routeTestResult{}, autoStopped
	}
	if ctx.Err() != nil {
		err, duration, measured.Duration = ctx.Err(), 0, 0
	}
	// Per-sample exit data is not part of the latency series.
	measured.ExitIP, measured.ExitError, measured.ExitLocation, measured.LastExitLocation = "", "", nil, nil
	if epoch != "" {
		latest, sampleErr := c.physical.sample(lifetime)
		if sampleErr != nil || latest.Epoch != epoch {
			return routeTestResult{}, autoDropped
		}
	}
	result := routeTestResult{routeName: target.record.Config.Name, at: time.Now(), network: epoch, success: err == nil, duration: duration, revision: target.record.Revision, configRevision: target.version, measurement: measured}
	if err != nil {
		failure := testErrorCode(err, ctx)
		result.code = failure.Error()
		var product *productError
		if errors.As(failure, &product) {
			result.code = product.code
		}
	}
	return result, ""
}

// tryAcquireLatency takes one shared latency slot without waiting.
func (c *Controller) tryAcquireLatency(owner *testOwner, limit int) bool {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.bandwidth == "network_diagnostics" || owner.activeLatency >= limit {
		return false
	}
	owner.activeLatency++
	return true
}
