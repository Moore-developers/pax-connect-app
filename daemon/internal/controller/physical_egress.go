package controller

import (
	"context"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/cadence"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
	"pax-connect/daemon/internal/network"
	"pax-connect/daemon/internal/telemetry"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// physicalSampleTick is how often the observer republishes the local sample. Downstream
// readers (stable-interval and freshness checks in current_path_*) assume a sample at most
// pathSampleFreshness old; while the route subscription is healthy the tick renews the last
// verified sample without running /sbin/route, so real sampling is event-driven.
const physicalSampleTick = pathStableInterval

type physicalOwner struct {
	local   localNetworkSample
	changed chan struct{}
	mu      sync.Mutex
	current *paxv1.PhysicalEgress
	sample  func(context.Context) (network.Physical, error)
	done    chan struct{}
}

// PhysicalOption customizes ConfigurePhysicalEgress.
type PhysicalOption func(*physicalOptions)

type physicalOptions struct {
	routes      func(context.Context) *network.RouteWatch
	externalTUN ExternalTUNAssess
}

// WithRouteWatch makes the observer sample on routing changes (started with the observer's
// lifetime) instead of polling; a failed subscription falls back to polling.
func WithRouteWatch(start func(context.Context) *network.RouteWatch) PhysicalOption {
	return func(o *physicalOptions) { o.routes = start }
}

// ConfigurePhysicalEgressCache is configured before the physical observer starts.
func (c *Controller) ConfigurePhysicalEgressCache(store *config.PhysicalEgressCacheStore) {
	c.physicalCache = store
}

// ConfigurePhysicalEgress installs a single daemon-owned network observer; testing injects
// native-network and HTTP boundaries. Without a route watch (or after it is lost) it polls
// every physicalSampleTick, as before.
func (c *Controller) ConfigurePhysicalEgress(sample func(context.Context) (network.Physical, error), probe func(context.Context, network.Physical) (string, string, string), options ...PhysicalOption) {
	var opt physicalOptions
	for _, apply := range options {
		apply(&opt)
	}
	p := &physicalOwner{sample: sample, current: &paxv1.PhysicalEgress{Phase: "loading"}, done: make(chan struct{}), changed: make(chan struct{})}
	c.physical = p
	go func() {
		defer close(p.done)
		stable := ""
		misses := 0
		var cached *paxv1.PhysicalEgress
		cacheWritable := true
		if c.physicalCache != nil {
			value, exists, err := c.physicalCache.Load()
			if err != nil {
				cacheWritable = false
				telemetry.RecordDiagnostic(telemetry.Diagnostic{Stage: "physical_egress_cache", Code: "cache_read_failed"})
			}
			if exists && err == nil {
				cached = &paxv1.PhysicalEgress{Network: value.Network, At: timestamppb.New(value.At), Ipv4: value.IPv4, Ipv6: value.IPv6, Phase: "ready"}
			}
		}

		type probeResult struct {
			epoch, v4, v6, code string
			at                  time.Time
		}
		completed := make(chan probeResult, 1)
		probeDone := make(chan struct{}, 1)
		var probeCancel context.CancelFunc
		watchCtx, stopWatch := context.WithCancel(c.proxy.lifetime)
		routeWake := make(chan struct{}, 1)
		var routeEvent, subscribed atomic.Bool
		forwarded := make(chan struct{})
		close(forwarded)
		if opt.routes != nil {
			watch := opt.routes(watchCtx)
			// Opening is synchronous, so the very first round already knows the outcome.
			select {
			case <-watch.Lost():
			default:
				subscribed.Store(true)
				telemetry.RecordDiagnostic(telemetry.Diagnostic{Stage: "physical_route_watch", Code: "subscribed", Success: true})
			}
			forwarded = make(chan struct{})
			go func() {
				defer close(forwarded)
				for {
					select {
					case <-watchCtx.Done():
						return
					case <-watch.Events():
						routeEvent.Store(true)
						select {
						case routeWake <- struct{}{}:
						default:
						}
					case <-watch.Lost():
						subscribed.Store(false)
						telemetry.RecordDiagnostic(telemetry.Diagnostic{Stage: "physical_route_watch", Code: "subscription_lost", Reason: watch.Err().Error()})
						select {
						case routeWake <- struct{}{}:
						default:
						}
						return
					}
				}
			}()
			defer func() { <-watch.Done() }()
		}
		defer func() {
			if probeCancel != nil {
				probeCancel()
				<-completed
			}
			stopWatch()
			<-forwarded
		}()

		triggerExternal, stopExternal := func() {}, func() {}
		if opt.externalTUN != nil {
			triggerExternal, stopExternal = c.startExternalTUN(c.proxy.lifetime, opt.externalTUN)
		}
		defer stopExternal()
		lastExternal := time.Time{}
		signalNetwork := func() {
			select {
			case c.proxy.networkSignal <- struct{}{}:
			default:
			}
		}
		var lastPhysical network.Physical
		var lastErr error
		var lastReal time.Time
		// observe runs the real native sample; reason names why, for field diagnosis.
		observe := func(reason string) (network.Physical, error) {
			sampled, stop := context.WithTimeout(c.proxy.lifetime, 2*time.Second)
			started := time.Now()
			physical, err := sample(sampled)
			stop()
			previous := lastPhysical.Epoch
			lastPhysical, lastErr, lastReal = physical, err, time.Now()
			p.publishLocalSample(physical, err)
			signalNetwork()
			if reason == "route_event" || reason == "fallback" {
				telemetry.RecordDiagnostic(telemetry.Diagnostic{Stage: "physical_sample", Code: reason, DurationMS: time.Since(started).Milliseconds(), Success: err == nil})
			}
			if err == nil && previous != "" && previous != physical.Epoch {
				telemetry.RecordDiagnostic(telemetry.Diagnostic{Stage: "physical_sample", Code: "epoch_changed", Phase: reason, Success: true})
			}
			return physical, err
		}
		retryAt := time.Time{}
		finish := func(value probeResult) {
			probeCancel()
			probeCancel = nil
			if value.epoch != stable {
				return
			}
			latest, e := observe("verify")
			if e != nil || latest.Epoch != stable {
				stable = ""
				return
			}
			result := &paxv1.PhysicalEgress{Network: stable, At: timestamppb.New(value.at), Ipv4: value.v4, Ipv6: value.v6, Phase: "ready"}
			if value.v4 == "" && value.v6 == "" {
				result.Phase = "failed"
				result.Error = &paxv1.ProductError{Code: value.code}
				retryAt = time.Now().Add(5 * time.Minute)
			} else {
				cached = result
				if c.physicalCache != nil && cacheWritable {
					saved, stop := context.WithTimeout(c.proxy.lifetime, time.Second)
					e := c.physicalCache.Save(saved, config.PhysicalEgressCache{Version: 1, Network: stable, At: value.at, IPv4: value.v4, IPv6: value.v6})
					stop()
					if e != nil {
						telemetry.RecordDiagnostic(telemetry.Diagnostic{Stage: "physical_egress_cache", Code: "cache_save_failed"})
					}
				}
			}
			c.publishPhysical(result)
		}
		round := func(context.Context) (bool, error) {
			select {
			case value := <-completed:
				finish(value)
			default:
			}
			event := routeEvent.Swap(false)
			reason := ""
			switch {
			case lastReal.IsZero():
				reason = "startup"
			case event:
				reason = "route_event"
			case !subscribed.Load():
				reason = "polling"
			case time.Since(lastReal) >= cadence.PhysicalEgress:
				reason = "fallback"
			case lastErr != nil && misses < 2:
				reason = "retry"
			}
			physical, err := lastPhysical, lastErr
			if reason != "" {
				// External TUN rides the observer's own triggers; "polling" (watch lost) is throttled to the fallback period.
				if reason == "startup" || reason == "route_event" || reason == "fallback" || reason == "polling" && time.Since(lastExternal) >= cadence.PhysicalEgress {
					lastExternal = time.Now()
					triggerExternal()
				}
				physical, err = observe(reason)
				if err != nil {
					misses++
				}
			} else {
				// No route event since the last verified sample: it is still the truth.
				p.publishLocalSample(physical, err)
				signalNetwork()
			}
			if err != nil {
				if misses >= 2 {
					stable = ""
					if probeCancel != nil {
						probeCancel()
					}
					c.publishPhysical(&paxv1.PhysicalEgress{Phase: "unavailable", Error: &paxv1.ProductError{Code: "physical_network_unavailable"}})
				}
			} else {
				misses = 0
				if stable != physical.Epoch {
					stable = physical.Epoch
					retryAt = time.Time{}
					if probeCancel != nil {
						probeCancel()
					}
					c.publishPhysical(physicalRefresh(stable, nil))
				}
				valid := cached != nil && cached.Network == stable && cached.At != nil && !time.Now().Before(cached.At.AsTime()) && time.Since(cached.At.AsTime()) < config.PhysicalEgressCacheTTL
				if valid {
					c.publishPhysical(cached)
				} else if probeCancel == nil && time.Now().After(retryAt) {
					task, cancel := context.WithTimeout(c.proxy.lifetime, measurement.ExitQueueTimeout+measurement.ExitTimeout)
					task = measurement.WithExitObserver(task, func(measurement.ExitAttempt) { c.scheduleMeasurementPublish() })
					probeCancel = cancel
					go func(p network.Physical) {
						v4, v6, code := probe(task, p)
						completed <- probeResult{p.Epoch, v4, v6, code, time.Now()}
						select {
						case probeDone <- struct{}{}:
						default:
						}
					}(physical)
				}
			}
			return false, nil
		}
		cadence.Loop{
			Name:     "physical_egress",
			Interval: func() time.Duration { return physicalSampleTick },
			Wake:     []<-chan struct{}{routeWake, probeDone},
			RunFirst: true,
			Run:      round,
		}.Serve(c.proxy.lifetime)
	}()
}

// waitEpochChange blocks until the owner publishes a sample taken after since whose epoch
// differs from epoch (or is unavailable). It reports false when ctx ends first, so a task
// can follow the physical path without sampling it on its own clock.
func (p *physicalOwner) waitEpochChange(ctx context.Context, epoch string, since time.Time) bool {
	for {
		sample, changed := p.localSnapshot()
		if sample.generation != 0 && sample.at.After(since) && (!sample.valid || sample.epoch != epoch) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-changed:
		}
	}
}

func (c *Controller) publishPhysical(result *paxv1.PhysicalEgress) {
	if c.tests != nil {
		c.tests.mu.Lock()
		var cancels []context.CancelFunc
		for _, p := range c.tests.progress {
			if p.Network != "" && p.Network != result.Network {
				c.tests.networkChanged[p.OperationId] = true
				if cancel := c.tests.ops[p.OperationId]; cancel != nil {
					cancels = append(cancels, cancel)
				}
			}
		}
		c.tests.mu.Unlock()
		for _, cancel := range cancels {
			cancel()
		}
	}

	c.physical.mu.Lock()
	if proto.Equal(c.physical.current, result) {
		c.physical.mu.Unlock()
		return
	}
	c.physical.current = result
	c.physical.mu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.current.PhysicalEgress = proto.Clone(result).(*paxv1.PhysicalEgress)
	c.commitLocked()
}
func (c *Controller) GetPhysicalEgress(context.Context, *emptypb.Empty) (*paxv1.PhysicalEgress, error) {
	if c.physical == nil {
		return &paxv1.PhysicalEgress{Phase: "unavailable"}, nil
	}
	c.physical.mu.Lock()
	value := proto.Clone(c.physical.current).(*paxv1.PhysicalEgress)
	c.physical.mu.Unlock()
	return value, nil
}

// NewPhysicalEgressProbe shares provider policy with route exits, while the client
// is explicitly bound to the physical interface and excludes system proxies.
func NewPhysicalEgressProbe(lookup *measurement.ExitLookup) func(context.Context, network.Physical) (string, string, string) {
	return func(ctx context.Context, p network.Physical) (string, string, string) {
		v4, v6, last := "", "", ""
		type familyResult struct{ family, ip, code string }
		results := make(chan familyResult, 2)
		count := 0
		for _, family := range []string{"4", "6"} {
			if family == "6" && !p.IPv6 {
				continue
			}
			count++
			go func(family string) {
				client, closeClient := network.PhysicalClient(p, family)
				defer closeClient()
				task := measurement.WithExitObserver(ctx, func(v measurement.ExitAttempt) {
					if v.Event == "attempt_complete" {
						telemetry.RecordDiagnostic(telemetry.Diagnostic{Stage: "physical_exit_provider", Phase: v.Provider, Code: v.Code, Reason: strconv.Itoa(v.HTTPStatus), DurationMS: v.Duration.Milliseconds(), Success: v.Code == ""})
					}
				})
				value, code := lookup.Query(task, client, measurement.ExitQuery{Scope: "physical/" + p.Epoch + "/" + family, Purpose: "physical_ip"})
				results <- familyResult{family, value.IP, code}
			}(family)
		}
		for i := 0; i < count; i++ {
			value := <-results
			if value.code != "" {
				last = value.code
			} else if value.family == "4" {
				v4 = value.ip
			} else {
				v6 = value.ip
			}
		}

		return v4, v6, last
	}
}

// Same-network revalidation retains the session cache; another network never inherits its IP.
func physicalRefresh(epoch string, cached *paxv1.PhysicalEgress) *paxv1.PhysicalEgress {
	if cached != nil && cached.Network == epoch && cached.Phase == "ready" {
		return proto.Clone(cached).(*paxv1.PhysicalEgress)
	}
	return &paxv1.PhysicalEgress{Network: epoch, Phase: "loading"}
}
