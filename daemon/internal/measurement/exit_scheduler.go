package measurement

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// ExitPoolState is a read-only, bounded count snapshot. It contains no addresses.
type ExitPoolState struct {
	ConfiguredLimit, InFlight, Queued, Retrying, AvailableProviders, TotalProviders int
	At                                                                              time.Time
}
type exitLease struct {
	provider           exitProvider
	quota, path, guard *providerHealth
	alias              bool
	chargedAt          time.Time
}

type exitWaiter struct {
	cfAlias        bool
	provider       string // Exact provider for diagnostic collection; never changes discovery order.
	scope, purpose string
	start          int
	retry, geo     bool
	tried          map[string]bool
}

// SetLimit applies a committed policy to future dispatches. Older revisions are
// ignored, and applying a newer policy never cancels an existing lease.
func (q *ExitLookup) SetLimit(limit int, revision uint64) {
	q.mu.Lock()
	if revision < q.policyRevision {
		q.mu.Unlock()
		return
	}
	q.policyRevision = revision
	q.limit = max(1, min(32, limit))
	q.wake()
	q.mu.Unlock()
}
func (q *ExitLookup) wake() { close(q.changed); q.changed = make(chan struct{}) }

// Snapshot reads real requests and waiters, rather than the number of tested routes.
func (q *ExitLookup) Snapshot() ExitPoolState {
	q.mu.Lock()
	defer q.mu.Unlock()
	state := ExitPoolState{ConfiguredLimit: q.limit, InFlight: q.inflight, Queued: len(q.queue), TotalProviders: len(q.providers)}
	for _, w := range q.queue {
		if w.retry {
			state.Retrying++
		}
	}
	for _, p := range q.providers {
		ready, _, _ := q.permit(p, "", false)
		if ready {
			state.AvailableProviders++
		}
	}
	previous := q.state
	previous.At = time.Time{}
	if previous != state {
		state.At = q.now()
		q.state = state
	} else {
		state.At = q.state.At
	}
	return state
}
func (q *ExitLookup) event(ctx context.Context, event string, attempt int) {
	observeExit(ctx, ExitAttempt{Event: event, Attempt: attempt, Pool: q.Snapshot()})
}
func (q *ExitLookup) remove(w *exitWaiter) {
	for i, item := range q.queue {
		if item == w {
			q.queue = append(q.queue[:i], q.queue[i+1:]...)
			q.wake()
			return
		}
	}
}

// acquire is the only dispatch gate, including hedges and exact-IP enrichment.
// FIFO contenders sleep on releases/limit changes or the nearest rate reset.
func (q *ExitLookup) acquire(ctx context.Context, w *exitWaiter, opportunistic bool) (exitLease, string) {
	q.mu.Lock()
	if !opportunistic {
		if len(q.queue) >= 128 {
			q.mu.Unlock()
			return exitLease{}, "exit_queue_full"
		}
		q.queue = append(q.queue, w)
		q.wake()
	}
	q.mu.Unlock()
	if !opportunistic {
		phase := "exit_queued"
		if w.retry {
			phase = "exit_retrying"
		}
		q.event(ctx, phase, len(w.tried))
	}
	defer func() {
		if !opportunistic {
			q.mu.Lock()
			q.remove(w)
			q.mu.Unlock()
			q.event(ctx, "pool_changed", len(w.tried))
		}
	}()
	for {
		if ctx.Err() != nil {
			return exitLease{}, cancelCode(ctx)
		}
		q.mu.Lock()
		p, ready, next, possible, awaitRelease := q.choose(w)
		earlier := false
		for _, item := range q.queue {
			if item == w {
				break
			}
			_, ok, _, _, _ := q.choose(item)
			if ok {
				earlier = true
				break
			}
		}
		if ready && !earlier && q.inflight < q.limit {
			lease, ok := q.reserve(p, w.scope)
			if !ok {
				q.mu.Unlock()
				return exitLease{}, "exit_services_unavailable"
			}
			lease.alias = w.cfAlias && p.id == "cloudflare"
			q.remove(w)
			q.mu.Unlock()
			return lease, ""
		}
		if opportunistic {
			q.mu.Unlock()
			return exitLease{}, "exit_provider_busy"
		}
		changed := q.changed
		if !possible {
			q.mu.Unlock()
			return exitLease{}, "exit_services_unavailable"
		}
		if deadline, ok := ctx.Deadline(); ok && !ready && !awaitRelease && !next.IsZero() && time.Until(deadline) < next.Sub(q.now()) {
			q.mu.Unlock()
			return exitLease{}, "exit_services_unavailable"
		}
		var timer *time.Timer
		var tick <-chan time.Time
		if !next.IsZero() {
			timer = time.NewTimer(max(time.Millisecond, next.Sub(q.now())))
			tick = timer.C
		}
		q.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-changed:
		case <-tick:
		}
		if timer != nil {
			timer.Stop()
		}
	}
}
func (q *ExitLookup) choose(w *exitWaiter) (exitProvider, bool, time.Time, bool, bool) {
	if w.provider != "" {
		p, ok := diagnosticProvider(w.provider)
		if !ok {
			return exitProvider{}, false, time.Time{}, false, false
		}
		ready, at, possible := q.permit(p, w.scope, true)
		return p, ready, at, possible, possible && !ready && at.IsZero()
	}
	var next time.Time
	possible := false
	awaitRelease := false
	for tier := 0; tier <= 2; tier++ {
		for i := 0; i < len(q.providers); i++ {
			p := q.providers[(w.start+i)%len(q.providers)]
			if p.tier != tier || w.tried[p.id] || (w.geo && p.id == "cloudflare") {
				continue
			}
			ready, at, can := q.permit(p, w.scope, true)
			if ready {
				return p, true, time.Time{}, true, false
			}
			possible = possible || can
			if can && at.IsZero() {
				awaitRelease = true
			}
			if can && !at.IsZero() && (next.IsZero() || at.Before(next)) {
				next = at
			}
		}
	}
	return exitProvider{}, false, next, possible, awaitRelease
}
func (q *ExitLookup) healthEntry(key string) *providerHealth {
	if value := q.health[key]; value != nil {
		return value
	}
	if len(q.health) >= 256 {
		now := q.now().UTC()
		for key, state := range q.health {
			id, _, _ := strings.Cut(key, "/")
			var provider exitProvider
			for _, candidate := range q.providers {
				if candidate.id == id {
					provider = candidate
					break
				}
			}
			expireProviderUsage(provider, state, now)
			if key == id || key == id+"/unknown" || key == id+"/path/unknown" {
				continue
			}
			if state.inflight == 0 && state.calls == 0 && !now.Before(state.until) && len(state.recent) == 0 {
				delete(q.health, key)
			}
		}
	}

	if len(q.health) >= 256 {
		return nil
	}
	value := &providerHealth{}
	q.health[key] = value
	return value
}
func (q *ExitLookup) quota(p exitProvider, scope string) *providerHealth {
	group := q.sources[p.id+"/"+scope]
	if group == "" {
		group = "unknown"
	}
	state := q.healthEntry(p.id + "/" + group)
	if state == nil {
		state = q.health[p.id+"/unknown"]
	}
	return state
}
func (q *ExitLookup) path(p exitProvider, scope string) *providerHealth {
	if group := q.sources[p.id+"/"+scope]; group != "" {
		scope = "source/" + group
	}
	state := q.healthEntry(p.id + "/path/" + scope)
	return state
}
func (q *ExitLookup) permit(p exitProvider, scope string, local bool) (bool, time.Time, bool) {
	now := q.now().UTC()
	global := q.health[p.id]
	var at time.Time
	for _, state := range []*providerHealth{global, q.quota(p, scope)} {
		if now.Before(state.until) && state.until.After(at) {
			at = state.until
		}
	}
	if local {
		state := q.path(p, scope)
		if state == nil {
			return false, time.Time{}, true
		}
		if now.Before(state.until) && state.until.After(at) {
			at = state.until
		}
	}
	quota := q.quota(p, scope)
	expireProviderUsage(p, quota, now)
	if p.daily > 0 && quota.calls >= p.daily {
		reset := quotaReset(p, quota.started)
		if reset.After(at) {
			at = reset
		}
	}

	for _, window := range p.windows {
		count := 0
		var oldest time.Time
		for _, t := range quota.recent {
			if now.Sub(t) < window.window {
				count++
				if oldest.IsZero() || t.Before(oldest) {
					oldest = t
				}
			}
		}
		if count >= window.limit {
			reset := oldest.Add(window.window)
			if reset.After(at) {
				at = reset
			}
		}
	}
	if !at.IsZero() {
		return false, at, true
	}
	if global.inflight >= p.slots || global.probing {
		return false, time.Time{}, true
	}
	return true, time.Time{}, true
}
func (q *ExitLookup) reserve(p exitProvider, scope string) (exitLease, bool) {
	ready, _, _ := q.permit(p, scope, true)
	if !ready {
		return exitLease{}, false
	}
	global := q.health[p.id]
	global.inflight++
	if !global.until.IsZero() {
		global.probing = true
	}
	local := q.path(p, scope)
	local.inflight++
	quota := q.quota(p, scope)
	guard := q.health[p.id+"/unknown"]
	chargedAt := q.now()
	expireProviderUsage(p, guard, chargedAt)
	chargeProviderUsage(p, guard, chargedAt)
	if quota != guard {
		chargeProviderUsage(p, quota, chargedAt)
	}
	q.inflight++
	q.wake()
	return exitLease{provider: p, quota: quota, path: local, guard: guard, chargedAt: chargedAt}, true
}
func (q *ExitLookup) finish(lease exitLease, scope string, value ExitResult, code string, status int, retry time.Duration, ignore bool) {
	p := lease.provider
	q.mu.Lock()
	defer q.mu.Unlock()
	defer q.wake()
	now := q.now().UTC()
	global := q.health[p.id]
	halfOpen := global.probing
	global.inflight--
	global.probing = false
	q.inflight--
	local := lease.path
	local.inflight--
	quota := lease.quota
	quota.inflight--
	if lease.guard != quota {
		lease.guard.inflight--
	}
	if code == "" {
		global.failures = 0
		local.failures = 0
		if !now.Before(global.until) {
			global.until = time.Time{}
		}
		if !now.Before(local.until) {
			local.until = time.Time{}
		}
		// Only self-observation learns the source. Each lease moves its own charge,
		// never the whole shared unknown bucket or an unrelated geo target.
		if value.IP != "" {
			group := sourceGroup(value.IP)
			key := p.id + "/" + scope
			known := q.healthEntry(p.id + "/" + group)
			if known != nil && known != quota {
				expireProviderUsage(p, known, now)
				// Move only the inferred source charge; the shared uncertainty guard must
				// retain every dispatch so new scopes cannot replenish anonymous capacity.
				if quota != lease.guard {
					if quota.calls > 0 {
						quota.calls--
					}
					for i, at := range quota.recent {
						if at.Equal(lease.chargedAt) {
							quota.recent = append(quota.recent[:i], quota.recent[i+1:]...)
							break
						}
					}
				}
				if p.daily == 0 || now.Before(quotaReset(p, lease.chargedAt)) {
					known.calls++
					if known.started.IsZero() || lease.chargedAt.Before(known.started) {
						known.started = lease.chargedAt.UTC()
					}
				}
				if len(p.windows) > 0 && now.Sub(lease.chargedAt) < time.Minute {
					known.recent = append(known.recent, lease.chargedAt)
				}
			}

			if known != nil {
				if len(q.sources) >= 128 && q.sources[key] == "" {
					for k := range q.sources {
						delete(q.sources, k)
						break
					}
				}
				q.sources[key] = group
			}
		}

		return
	}
	if ignore {
		return
	}
	if status == http.StatusTooManyRequests {
		if retry <= 0 {
			retry = time.Minute
		}
		if p.utcDay && p.daily > 0 {
			reset := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC).Sub(now)
			retry = max(retry, reset)
		}
		quota.until = maxTime(quota.until, now.Add(retry))
		return
	}
	if code == "exit_provider_auth_required" {
		global.until = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
		return
	}
	if status == http.StatusForbidden {
		local.until = now.Add(5 * time.Minute)
		return
	}
	state := local
	if status >= 500 || code == "exit_response_invalid" {
		if halfOpen {
			global.until = now.Add(time.Minute)
			global.failures = 0
			return
		}
		state = global
	}
	state.failures++
	if state.failures >= 3 {
		state.until = now.Add(time.Minute)
		state.failures = 0
	}
}
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func quotaReset(p exitProvider, started time.Time) time.Time {
	if p.utcDay {
		started = started.UTC()
		return time.Date(started.Year(), started.Month(), started.Day()+1, 0, 0, 0, 0, time.UTC)
	}
	return started.Add(24 * time.Hour)
}
func expireProviderUsage(p exitProvider, state *providerHealth, now time.Time) {
	var longest time.Duration
	for _, window := range p.windows {
		longest = max(longest, window.window)
	}
	kept := state.recent[:0]
	for _, at := range state.recent {
		if now.Sub(at) < longest {
			kept = append(kept, at)
		}
	}
	state.recent = kept
	if p.daily > 0 {
		if state.started.IsZero() || !now.Before(quotaReset(p, state.started)) {
			state.started = time.Time{}
			state.calls = 0
		}
	} else if len(state.recent) == 0 && state.inflight == 0 {
		state.calls = 0
		state.started = time.Time{}
	}
}

func chargeProviderUsage(p exitProvider, state *providerHealth, at time.Time) {
	state.inflight++
	if state.started.IsZero() {
		state.started = at.UTC()
	}
	state.calls++
	if len(p.windows) > 0 {
		state.recent = append(state.recent, at)
	}
}
