package environment

import (
	"context"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"pax-connect/daemon/internal/cadence"
)

// Flow is the read-only slice of a live connection fact that STUN route evidence needs.
type Flow struct {
	IP                                                         string
	Port                                                       uint16
	Transport, Direction, RouteName, RuleSource, Rule, Process string
	StartedAt                                                  time.Time
}

// EvidenceProvider is injected by the controller; the environment package never imports it.
// RouteStun returns route_stun checks for flows started within [from, to]. PhysicalEgress
// returns the current physical-egress check. Both are read-only.
type EvidenceProvider interface {
	RouteStun(from, to time.Time) []Check
	PhysicalEgress() Check
}

const maxRouteStun = 4

// Variables so tests can shrink the polling schedule.
var (
	routePollInterval  = 500 * time.Millisecond
	routeCollectWindow = cadence.HistoryIngest + time.Second
)

// RouteStunChecks builds the route_stun checks. available=false means no live view exists.
func RouteStunChecks(flows []Flow, available bool, from, to time.Time) []Check {
	if !available {
		return []Check{{Kind: "route_stun", Target: "stun", State: "unavailable", ErrorCode: "live_view_unavailable", At: time.Now()}}
	}
	sort.SliceStable(flows, func(i, j int) bool { return flows[i].StartedAt.Before(flows[j].StartedAt) })
	seen := map[string]bool{}
	var out []Check
	for _, f := range flows {
		if f.StartedAt.Before(from) || f.StartedAt.After(to) || !strings.EqualFold(f.Transport, "udp") {
			continue
		}
		if f.Port != 3478 && f.Port != 19302 && !strings.Contains(f.RuleSource, "WebRTC出口限制") {
			continue
		}
		target := net.JoinHostPort(f.IP, strconv.Itoa(int(f.Port)))
		if seen[target] || len(out) >= maxRouteStun {
			continue
		}
		seen[target] = true
		state := f.Direction
		switch state {
		case "proxy", "direct", "blocked":
		default:
			state = "unknown"
		}
		out = append(out, Check{Kind: "route_stun", Target: target, State: state, Value: strings.Join([]string{f.RouteName, f.RuleSource, f.Rule, f.Process}, "\t"), At: f.StartedAt})
	}
	if len(out) == 0 {
		return []Check{{Kind: "route_stun", Target: "stun", State: "unobserved", At: time.Now()}}
	}
	return out
}

func stunPortsSeen(checks []Check) bool {
	var p3478, p19302 bool
	for _, c := range checks {
		if c.State != "proxy" && c.State != "direct" && c.State != "blocked" {
			continue
		}
		p3478 = p3478 || strings.HasSuffix(c.Target, ":3478")
		p19302 = p19302 || strings.HasSuffix(c.Target, ":19302")
	}
	return p3478 && p19302
}

// collectRouteEvidence polls the live view until both STUN ports show an outbound direction, or the
// window ends or ctx is done, then returns route_stun plus egress_physical checks.
func (s *Session) collectRouteEvidence(ctx context.Context, provider EvidenceProvider, from, returned time.Time) []Check {
	if provider == nil {
		return nil
	}
	to := returned.Add(time.Second)
	deadline := time.NewTimer(routeCollectWindow)
	defer deadline.Stop()
	tick := time.NewTicker(routePollInterval)
	defer tick.Stop()
	for {
		checks := provider.RouteStun(from, to)
		if len(checks) > 0 && checks[0].State == "unavailable" {
			return append(checks, provider.PhysicalEgress())
		}
		if stunPortsSeen(checks) {
			return append(checks, provider.PhysicalEgress())
		}
		select {
		case <-ctx.Done():
			return append(checks, provider.PhysicalEgress())
		case <-deadline.C:
			return append(provider.RouteStun(from, to), provider.PhysicalEgress())
		case <-tick.C:
		}
	}
}
