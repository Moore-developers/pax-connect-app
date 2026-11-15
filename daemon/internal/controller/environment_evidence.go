package controller

import (
	"time"

	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/environment"
)

// environmentEvidence feeds the environment session read-only route and physical-egress facts.
// It opens no connection and runs no probe.
type environmentEvidence struct{ c *Controller }

func (e environmentEvidence) RouteStun(from, to time.Time) []environment.Check {
	live := e.c.liveView.Load()
	if live == nil || !e.c.protectionOn() {
		return environment.RouteStunChecks(nil, false, from, to)
	}
	views := live.Flows(time.Now())
	flows := make([]environment.Flow, 0, len(views))
	for _, v := range views {
		f := v.Fact
		flows = append(flows, environment.Flow{IP: f.IP, Port: f.Port, Transport: f.Transport, Direction: f.Direction, RouteName: f.RouteName, RuleSource: f.RuleSource, Rule: f.Rule, Process: f.Process, StartedAt: f.StartedAt})
	}
	return environment.RouteStunChecks(flows, true, from, to)
}

func (e environmentEvidence) PhysicalEgress() environment.Check {
	check := environment.Check{Kind: "egress_physical", Target: "physical", State: "unavailable", At: time.Now()}
	if e.c.physical == nil {
		return check
	}
	e.c.physical.mu.Lock()
	value := e.c.physical.current
	phase, v4, v6 := "", "", ""
	var at time.Time
	if value != nil {
		phase, v4, v6 = value.Phase, value.Ipv4, value.Ipv6
		if value.At != nil {
			at = value.At.AsTime()
		}
	}
	e.c.physical.mu.Unlock()
	if phase != "ready" {
		return check
	}
	check.State, check.Value = "observed", "v4="+v4+";v6="+v6
	if !at.IsZero() {
		check.At = at
	}
	return check
}

// protectionOn reports whether Pax is intercepting traffic, from the same Snapshot status GetSnapshot returns.
// The lock is held only for the read.
func (c *Controller) protectionOn() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current != nil && (c.current.Status == paxv1.ProtectionStatus_PROTECTION_STATUS_PROTECTED || c.current.Status == paxv1.ProtectionStatus_PROTECTION_STATUS_DEGRADED)
}

// DNSPath correlates only query names minted by this environment session.
func (e environmentEvidence) DNSPath(names []string, from, to time.Time) []environment.Check {
	live := e.c.liveView.Load()
	if live == nil || !e.c.protectionOn() {
		return environment.DNSPathChecks(names, nil, false, from, to)
	}
	views := live.Flows(time.Now())
	flows := make([]environment.DNSFlow, 0, len(views))
	for _, v := range views {
		f := v.Fact
		if f.Purpose == "access" {
			if f.Direction != "proxy" || f.IPSource != "proxy_domain" {
				continue
			}
			flows = append(flows, environment.DNSFlow{Domain: f.Domain, StartedAt: f.StartedAt, UpstreamAt: f.StartedAt,
				Outbound: "proxy", RemoteRoute: f.RouteName, Pool: "remote", RemoteProxy: f.Direction == "proxy" && f.IPSource == "proxy_domain", Responded: f.ResponseKnown})
			continue
		}
		if f.Purpose != "dns" || f.DNS == nil {
			continue
		}
		d := f.DNS
		flows = append(flows, environment.DNSFlow{Domain: f.Domain, StartedAt: f.StartedAt, Upstream: d.Upstream, Outbound: d.Outbound, Pool: d.Pool, UpstreamAt: d.UpstreamAt})
	}
	return environment.DNSPathChecks(names, flows, true, from, to)
}
