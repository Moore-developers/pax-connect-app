package environment

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

func stunFlow(ip string, port uint16, at time.Time) Flow {
	return Flow{IP: ip, Port: port, Transport: "UDP", Direction: "proxy", RouteName: "HK", RuleSource: "Pax 包：WebRTC出口限制", Rule: "r", Process: "Chrome", StartedAt: at}
}

func TestRouteStunWindowPortRuleAndLimit(t *testing.T) {
	from, to := t0, t0.Add(10*time.Second)
	flows := []Flow{
		stunFlow("1.1.1.1", 3478, t0.Add(-time.Second)), // before window
		stunFlow("1.1.1.2", 3478, to.Add(time.Second)),  // after window
		{IP: "2.2.2.2", Port: 443, Transport: "tcp", Direction: "proxy", StartedAt: t0.Add(time.Second)},
		{IP: "2.2.2.3", Port: 443, Transport: "udp", Direction: "proxy", StartedAt: t0.Add(time.Second)}, // wrong port, no rule
		{IP: "3.3.3.3", Port: 5000, Transport: "udp", Direction: "direct", RuleSource: "Pax 包：WebRTC出口限制", StartedAt: t0.Add(time.Second)},
		{IP: "4.4.4.4", Port: 19302, Transport: "udp", Direction: "weird", StartedAt: t0.Add(2 * time.Second)},
		stunFlow("4.4.4.4", 19302, t0.Add(3*time.Second)), // duplicate target
		{IP: "2001:db8::1", Port: 3478, Transport: "udp", Direction: "blocked", StartedAt: t0.Add(4 * time.Second)},
		stunFlow("5.5.5.5", 3478, t0.Add(5*time.Second)),
		stunFlow("6.6.6.6", 3478, t0.Add(6*time.Second)), // over the cap of 4
	}
	got := RouteStunChecks(flows, true, from, to)
	if len(got) != 4 {
		t.Fatalf("want 4 checks, got %+v", got)
	}
	want := []struct{ target, state string }{{"3.3.3.3:5000", "direct"}, {"4.4.4.4:19302", "unknown"}, {"[2001:db8::1]:3478", "blocked"}, {"5.5.5.5:3478", "proxy"}}
	for i, w := range want {
		if got[i].Kind != "route_stun" || got[i].Target != w.target || got[i].State != w.state {
			t.Fatalf("check %d = %+v, want %+v", i, got[i], w)
		}
	}
	if got[3].Value != "HK\tPax 包：WebRTC出口限制\tr\tChrome" || !got[3].At.Equal(t0.Add(5*time.Second)) {
		t.Fatalf("value/at wrong: %+v", got[3])
	}
}

func TestRouteStunUnobservedAndUnavailable(t *testing.T) {
	got := RouteStunChecks(nil, true, t0, t0.Add(time.Second))
	if len(got) != 1 || got[0].State != "unobserved" || got[0].Target != "stun" {
		t.Fatalf("%+v", got)
	}
	got = RouteStunChecks(nil, false, t0, t0.Add(time.Second))
	if len(got) != 1 || got[0].State != "unavailable" || got[0].ErrorCode != "live_view_unavailable" || got[0].Target != "stun" {
		t.Fatalf("%+v", got)
	}
}

type fakeProvider struct {
	mu     sync.Mutex
	calls  int
	appear int // call number from which both STUN ports are visible; 0 = never
}

func (f *fakeProvider) RouteStun(from, to time.Time) []Check {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.appear > 0 && f.calls >= f.appear {
		return []Check{{Kind: "route_stun", Target: "1.1.1.1:3478", State: "proxy"}, {Kind: "route_stun", Target: "8.8.8.8:19302", State: "proxy"}}
	}
	return []Check{{Kind: "route_stun", Target: "stun", State: "unobserved"}}
}
func (f *fakeProvider) PhysicalEgress() Check {
	return Check{Kind: "egress_physical", Target: "physical", State: "observed", Value: "v4=9.9.9.9;v6="}
}

func shrink(t *testing.T, poll, window time.Duration) {
	p, w := routePollInterval, routeCollectWindow
	routePollInterval, routeCollectWindow = poll, window
	t.Cleanup(func() { routePollInterval, routeCollectWindow = p, w })
}

func TestCollectRouteEvidenceEndsEarlyWhenBothPortsSeen(t *testing.T) {
	shrink(t, 5*time.Millisecond, 5*time.Second)
	f := &fakeProvider{appear: 3}
	start := time.Now()
	got := (&Session{}).collectRouteEvidence(context.Background(), f, t0, t0)
	if time.Since(start) > time.Second || f.calls != 3 || len(got) != 3 || got[2].Kind != "egress_physical" {
		t.Fatalf("early end failed: calls=%d elapsed=%v %+v", f.calls, time.Since(start), got)
	}
}

type unavailableProvider struct {
	fakeProvider
}

func (u *unavailableProvider) RouteStun(from, to time.Time) []Check {
	u.calls++
	return []Check{{Kind: "route_stun", Target: "stun", State: "unavailable"}}
}

func TestCollectRouteEvidenceReturnsImmediatelyWhenUnavailable(t *testing.T) {
	shrink(t, 5*time.Millisecond, 5*time.Second)
	u := &unavailableProvider{}
	start := time.Now()
	got := (&Session{}).collectRouteEvidence(context.Background(), u, t0, t0)
	if time.Since(start) > time.Second || u.calls != 1 || len(got) != 2 || got[0].State != "unavailable" || got[1].Kind != "egress_physical" {
		t.Fatalf("unavailable must return at once: calls=%d %+v", u.calls, got)
	}
}

func TestCollectRouteEvidenceStopsAtWindowAndContext(t *testing.T) {
	shrink(t, 5*time.Millisecond, 60*time.Millisecond)
	start := time.Now()
	got := (&Session{}).collectRouteEvidence(context.Background(), &fakeProvider{}, t0, t0)
	if e := time.Since(start); e < 50*time.Millisecond || e > time.Second || got[0].State != "unobserved" || got[len(got)-1].Kind != "egress_physical" {
		t.Fatalf("window end failed: %v %+v", e, got)
	}
	shrink(t, 5*time.Millisecond, time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start = time.Now()
	got = (&Session{}).collectRouteEvidence(ctx, &fakeProvider{}, t0, t0)
	if time.Since(start) > time.Second || len(got) != 2 {
		t.Fatalf("context end failed: %+v", got)
	}
	if (&Session{}).collectRouteEvidence(context.Background(), nil, t0, t0) != nil {
		t.Fatal("nil provider must add nothing")
	}
}

func TestBrowserCannotSubmitDaemonEvidenceKinds(t *testing.T) {
	for _, kind := range []string{"route_stun", "egress_physical"} {
		if validateChecks([]Check{{Kind: kind, Target: "stun", State: "observed", At: time.Now()}}) == nil {
			t.Fatalf("browser-submitted %s accepted", kind)
		}
	}
	long := strings.Repeat("x", 4096)
	if validateChecks([]Check{{Kind: "rtc_cf", Target: "stun.cloudflare.com:3478", State: "observed", Value: long, At: time.Now()}}) != nil {
		t.Fatal("4096-byte rtc value must stay valid")
	}
}
