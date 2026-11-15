package controller

import (
	"fmt"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

func TestEnvironmentEvidenceRouteStunFromLiveView(t *testing.T) {
	c := &Controller{}
	e := environmentEvidence{c}
	now := time.Now()
	if got := e.RouteStun(now.Add(-time.Minute), now); got[0].State != "unavailable" {
		t.Fatalf("nil live view must be unavailable: %+v", got)
	}
	live := newLiveState()
	fact := config.FlowFact{Key: "k", IP: "1.1.1.1", Port: 3478, Transport: "udp", Direction: "proxy", RouteName: "HK", RuleSource: "Pax 包：WebRTC出口限制", Process: "Chrome", StartedAt: now.Add(-time.Second)}
	live.apply(config.ObservationBatch{Session: "s", Collecting: true, ResetToken: "t", ResetRequired: true, BaselineComplete: true, Baseline: []config.FlowFact{fact}}, now)
	c.liveView.Store(live)
	c.current = &paxv1.Snapshot{Status: paxv1.ProtectionStatus_PROTECTION_STATUS_OFF}
	if got := e.RouteStun(now.Add(-time.Minute), now); got[0].State != "unavailable" || got[0].ErrorCode != "live_view_unavailable" || got[0].Target != "stun" {
		t.Fatalf("protection off must be unavailable: %+v", got)
	}
	c.current.Status = paxv1.ProtectionStatus_PROTECTION_STATUS_PROTECTED
	got := e.RouteStun(now.Add(-time.Minute), now)
	if len(got) != 1 || got[0].Target != "1.1.1.1:3478" || got[0].State != "proxy" {
		t.Fatalf("%+v", got)
	}
	if got := e.RouteStun(now, now.Add(time.Second)); got[0].State != "unobserved" {
		t.Fatalf("flow outside window must be unobserved: %+v", got)
	}
}

func TestEnvironmentEvidencePhysicalEgress(t *testing.T) {
	c := &Controller{}
	e := environmentEvidence{c}
	if got := e.PhysicalEgress(); got.Kind != "egress_physical" || got.State != "unavailable" {
		t.Fatalf("%+v", got)
	}
	at := time.Now().Add(-time.Minute).Truncate(time.Second)
	c.physical = &physicalOwner{current: &paxv1.PhysicalEgress{Phase: "ready", Ipv4: "9.9.9.9", At: timestamppb.New(at)}}
	got := e.PhysicalEgress()
	if got.State != "observed" || got.Value != "v4=9.9.9.9;v6=" || !got.At.Equal(at) || got.Target != "physical" {
		t.Fatalf("%+v", got)
	}
	c.physical.current = &paxv1.PhysicalEgress{Phase: "loading"}
	if got := e.PhysicalEgress(); got.State != "unavailable" {
		t.Fatalf("%+v", got)
	}
}

func TestEnvironmentDNSPathUsesActualQueryProofAndRespectsOff(t *testing.T) {
	c := &Controller{}
	e := environmentEvidence{c}
	now := time.Now()
	names := make([]string, 12)
	facts := make([]config.FlowFact, 12)
	for i := range names {
		names[i] = fmt.Sprintf("name%d.fixture.bash.ws", i)
		facts[i] = config.FlowFact{Key: names[i], Purpose: "dns", Domain: names[i], StartedAt: now.Add(-time.Second), DNS: &config.DNSFact{Upstream: "1.1.1.1", Pool: "overseas", Outbound: "proxy", UpstreamAt: now}}
	}
	live := newLiveState()
	live.apply(config.ObservationBatch{Session: "s", Collecting: true, ResetRequired: true, ResetToken: "t", BaselineComplete: true, Baseline: facts}, now)
	c.liveView.Store(live)
	c.current = &paxv1.Snapshot{Status: paxv1.ProtectionStatus_PROTECTION_STATUS_OFF}
	if got := e.DNSPath(names, now.Add(-time.Minute), now.Add(time.Second)); got[0].State != "unavailable" {
		t.Fatal(got)
	}
	c.current.Status = paxv1.ProtectionStatus_PROTECTION_STATUS_PROTECTED
	if got := e.DNSPath(names, now.Add(-time.Minute), now.Add(time.Second)); got[0].State != "proxy" {
		t.Fatal(got)
	}
}

// Chrome hands CONNECT hostnames to the proxy; no local DNS query is generated.
// This is the same recorded shape as the real 01:28 Chrome probe flows.
func TestEnvironmentDNSPathRecognizesProxyDomainResponses(t *testing.T) {
	c := &Controller{current: &paxv1.Snapshot{Status: paxv1.ProtectionStatus_PROTECTION_STATUS_PROTECTED}}
	now := time.Now()
	names := make([]string, 12)
	facts := make([]config.FlowFact, 12)
	for i := range names {
		names[i] = fmt.Sprintf("remote%d.fixture.bash.ws", i)
		closed := now
		facts[i] = config.FlowFact{Key: names[i], Purpose: "access", Domain: names[i], Port: 443, Transport: "tcp", Direction: "proxy", RouteID: "pax-node-fixture", RouteName: "Fixture Route", IPSource: "proxy_domain", ResponseKnown: true, ResponseMS: 50, StartedAt: now.Add(-time.Second), ClosedAt: &closed}
	}
	live := newLiveState()
	live.apply(config.ObservationBatch{Session: "s", Collecting: true, ResetRequired: true, ResetToken: "t", BaselineComplete: true, Baseline: facts}, now)
	c.liveView.Store(live)
	got := environmentEvidence{c}.DNSPath(names, now.Add(-time.Minute), now.Add(time.Second))
	if got[0].State != "proxy" {
		t.Fatalf("Chrome handed all 12 exact domains to responding proxy flows, but paths were not recognized: %+v", got)
	}
}
