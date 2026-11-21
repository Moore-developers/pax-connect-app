package singbox

import (
	"context"
	"errors"
	"maps"
	"sync"
	"time"
)

var errFixedRouteUnavailable = errors.New("fixed_route_unavailable")

type coreRouteGateKey struct{}

// routeGate owns admission and in-flight cancellation, including flows between
// matching and native tracking. Lifecycle decisions belong to the root owner.
type routeGate struct {
	mu      sync.Mutex
	tags    map[string]bool
	blocked map[string]bool
	leases  map[*routeLease]string
	recent  map[string]time.Time
	version uint64
}

type routeLease struct {
	cancel context.CancelFunc
	close  func() error
}

func (g *routeGate) configure(tags map[string]bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tags = maps.Clone(tags)
	g.version++
	for tag := range g.blocked {
		if !tags[tag] {
			delete(g.blocked, tag)
		}
	}
	for tag := range g.recent {
		if !tags[tag] {
			delete(g.recent, tag)
		}
	}
}

func (g *routeGate) acquire(ctx context.Context, tag string, closeConnection func() error) (context.Context, func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.tags[tag] {
		return ctx, func() {}, nil
	}
	if g.recent == nil {
		g.recent = map[string]time.Time{}
	}
	g.recent[tag] = time.Now()
	if g.blocked[tag] {
		return ctx, func() {}, errFixedRouteUnavailable
	}
	ctx, cancel := context.WithCancel(ctx)
	lease := &routeLease{cancel: cancel, close: closeConnection}
	if g.leases == nil {
		g.leases = map[*routeLease]string{}
	}
	g.leases[lease] = tag
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			g.mu.Lock()
			delete(g.leases, lease)
			g.mu.Unlock()
			cancel()
		})
	}, nil
}

func (g *routeGate) setBlocked(tag string, blocked bool) {
	g.mu.Lock()
	if g.blocked == nil {
		g.blocked = map[string]bool{}
	}
	g.blocked[tag] = blocked
	g.version++
	var leases []*routeLease
	if blocked {
		for lease, owner := range g.leases {
			if owner == tag {
				leases = append(leases, lease)
			}
		}
	}
	g.mu.Unlock()
	// Closing may synchronously release a lease. Never call it under the mutex.
	for _, lease := range leases {
		lease.cancel()
		if lease.close != nil {
			_ = lease.close()
		}
	}
}

func (g *routeGate) reset() {
	g.mu.Lock()
	leases := g.leases
	g.tags, g.blocked, g.leases = nil, nil, nil
	g.recent = nil
	g.version++
	g.mu.Unlock()
	for lease := range leases {
		lease.cancel()
		if lease.close != nil {
			_ = lease.close()
		}
	}
}

// candidates includes quiet live connections, recent failed dials and every
// blocked route. Unused catalog entries produce no background requests.
func (g *routeGate) candidates(now time.Time) map[string]bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	result := map[string]bool{}
	for tag, at := range g.recent {
		if g.tags[tag] && now.Sub(at) < time.Minute {
			result[tag] = g.blocked[tag]
		}
	}
	for _, tag := range g.leases {
		if g.tags[tag] {
			result[tag] = g.blocked[tag]
		}
	}
	for tag, blocked := range g.blocked {
		if blocked && g.tags[tag] {
			result[tag] = true
		}
	}
	return result
}

func (g *routeGate) environment() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.version
}

func (g *routeGate) pending(tag string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	count := 0
	for _, owner := range g.leases {
		if owner == tag {
			count++
		}
	}
	return count
}
