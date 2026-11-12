package controller

import (
	"reflect"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
	"pax-connect/daemon/internal/stats"
)

func trafficFlow(key string, mutate func(*config.FlowFact)) *config.FlowFact {
	f := &config.FlowFact{Key: key, StartedAt: trafficEpoch, Direction: "proxy", RouteID: "route-a", RouteName: "Route A",
		Transport: "tcp", Rule: "domain=example.com", RuleSource: "user"}
	mutate(f)
	return f
}

func withClosed(f *config.FlowFact, at time.Time) *config.FlowFact {
	c := *f
	c.ClosedAt = &at
	return &c
}

func withFact(f *config.FlowFact, mutate func(*config.FlowFact)) *config.FlowFact {
	c := *f
	mutate(&c)
	return &c
}

// trafficEquivalenceFixture is one observation sequence covering: application, system, tool, unknown and
// DNS sources; direct and proxy routes and a flow without a route; started, ready, response, failure,
// rule rejection, byte samples across a 5-minute boundary, closing, latency and a later gap. Every flow
// keeps its attribution constant: a late upgrade is the one documented difference (spec §5).
func trafficEquivalenceFixture(h *trafficHarness) [][]config.Observation {
	appA := trafficFlow("s/1/appA", func(f *config.FlowFact) {
		f.ApplicationID, f.Application, f.Domain, f.IP = "com.a.app", "A", "api.example.com", "1.1.1.1"
	})
	system := trafficFlow("s/1/sys", func(f *config.FlowFact) {
		f.Process, f.Domain, f.IP, f.Direction, f.RouteID, f.RouteName = "/usr/libexec/trustd", "ocsp.apple.com", "2.2.2.2", "direct", "direct", "直连"
		f.Rule = "geosite=apple"
	})
	lost := trafficFlow("s/1/lost", func(f *config.FlowFact) {
		f.IP, f.Direction, f.RouteID, f.RouteName, f.Rule = "3.3.3.3", "unknown", "", "", ""
	})
	dns := trafficFlow("s/1/dns", func(f *config.FlowFact) {
		f.Purpose, f.Direction, f.RouteID, f.RouteName = "dns", "direct", "direct", "直连"
	})
	rejected := trafficFlow("s/1/rej", func(f *config.FlowFact) {
		f.ApplicationID, f.Application, f.Domain, f.RouteID, f.RouteName = "com.b.app", "B", "blocked.example.net", "route-b", "Route B"
	})
	tool := trafficFlow("s/1/tool", func(f *config.FlowFact) {
		f.Process, f.Domain, f.IP = "/opt/homebrew/bin/curl", "api.example.com", "1.1.1.1"
	})
	stranger := trafficFlow("s/1/stranger", func(f *config.FlowFact) {
		f.Domain, f.IP = "api.example.com", "4.4.4.4"
	})
	ready := func(f *config.FlowFact, ms uint64) *config.FlowFact {
		return withFact(f, func(c *config.FlowFact) { c.ResponseKnown, c.ResponseMS = true, ms })
	}
	establishedDNS := withFact(dns, func(f *config.FlowFact) { f.EstablishmentState, f.EstablishmentMS = "ready", 12 })
	failedLost := withFact(lost, func(f *config.FlowFact) { f.Failure = "dial_timeout" })
	rejectedFact := withFact(rejected, func(f *config.FlowFact) { f.EstablishmentState = "failed" })
	firstResponse := withFact(system, func(f *config.FlowFact) {
		f.EstablishmentState, f.Evidence, f.EstablishmentMS = "response_ready", "first_response", 35
	})
	s := time.Second
	first := []config.Observation{
		h.obs(0, "started", appA, 0, 0),
		h.obs(s, "started", system, 0, 0),
		h.obs(s, "started", lost, 0, 0),
		h.obs(2*s, "sample", appA, 100, 1000),
		h.obs(2*s, "started", dns, 0, 0),
		h.obs(3*s, "ready", ready(appA, 120), 0, 0),
		h.obs(3*s, "ready", establishedDNS, 0, 0),
		h.obs(3*s, "started", rejected, 0, 0),
		h.obs(4*s, "rejected", rejectedFact, 0, 0),
		h.obs(4*s, "sample", firstResponse, 30, 300),
		h.obs(5*s, "sample", lost, 7, 70),
		h.obs(5*s, "sample", establishedDNS, 10, 20),
		h.obs(6*s, "started", tool, 0, 0),
		h.obs(6*s, "failed", failedLost, 0, 0),
		h.obs(7*s, "started", stranger, 3, 3),
	}
	second := []config.Observation{
		h.obs(8*s, "sample", ready(appA, 120), 50, 500), // 08:04:58: still the first bucket
		h.obs(9*s, "ready", ready(tool, 80), 20, 200),
		h.obs(10*s, "sample", ready(appA, 125), 60, 600), // 08:05:00: second bucket
		h.obs(10*s, "ready", establishedDNS, 0, 0),       // the same DNS answer again: counted once
		h.obs(11*s, "sample", failedLost, 1, 1),          // the failure is repeated: counted once
		h.obs(12*s, "sample", ready(tool, 90), 25, 250),
		h.obs(13*s, "closed", withClosed(ready(appA, 125), trafficEpoch.Add(13*s)), 0, 0),
		h.obs(13*s, "closed", withClosed(firstResponse, trafficEpoch.Add(13*s)), 0, 0),
		h.obs(14*s, "closed", withClosed(failedLost, trafficEpoch.Add(14*s)), 0, 0),
		h.obs(14*s, "closed", withClosed(establishedDNS, trafficEpoch.Add(14*s)), 0, 0),
		h.obs(14*s, "closed", withClosed(rejectedFact, trafficEpoch.Add(14*s)), 0, 0),
		h.obs(15*s, "closed", withClosed(ready(tool, 90), trafficEpoch.Add(15*s)), 0, 0),
		h.obs(15*s, "closed", withClosed(stranger, trafficEpoch.Add(15*s)), 0, 0),
	}
	// An event without a flow and an observation without a time change nothing.
	second = append(second, config.Observation{Session: "s", Sequence: h.seq + 1, At: trafficEpoch.Add(16 * s), Kind: "event"})
	h.seq++
	return [][]config.Observation{first, second}
}

type rowView struct {
	Up, Down, Conns, Failed uint64
	Hist                    map[int]uint64
	Latest, Max             uint64
	LatestAtMs              int64
	Bases                   map[string]aggregateBasis
}

func viewOf(v aggregateBucket) rowView {
	r := rowView{Up: v.Upload, Down: v.Download, Conns: v.Connections, Failed: v.Failed, Hist: map[int]uint64{}, Latest: v.Latest,
		Max: v.Maximum, LatestAtMs: unixMilliOrZero(v.LatestAt), Bases: map[string]aggregateBasis{}}
	for key, basis := range v.Bases {
		basis.Contribution = nil
		r.Bases[key] = basis
	}
	for k, n := range v.Latency {
		r.Hist[k] = n
	}
	if len(r.Bases) == 0 {
		r.Bases = nil
	}
	return r
}

// legacyTrafficView compares the original golden contract. Match metadata has a
// separate oracle below; other users of viewOf still compare it without normalization.
func legacyTrafficView(v aggregateBucket) rowView {
	row := viewOf(v)
	for key, basis := range row.Bases {
		basis.MatchTracked = false
		basis.MatchUnknownUntil = time.Time{}
		row.Bases[key] = basis
	}
	return row
}

// The legacy golden has no decision-time evidence. Its read projection must mark
// every basis unknown through the bucket end, without inventing a latest match.
func assertLegacyMatchBounds(t *testing.T, key historystore.TrafficKey, old aggregateBucket) {
	t.Helper()
	for name, basis := range old.Bases {
		if basis.MatchTracked || !basis.MatchUnknownUntil.IsZero() || basis.LatestMatch != nil {
			t.Fatalf("legacy fixture %+v basis %q unexpectedly contains decision metadata", key, name)
		}
	}
	encoded, err := encodeTrafficRow(key, old)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeTrafficRow(encoded)
	if err != nil {
		t.Fatal(err)
	}
	until := time.UnixMilli(key.BucketMs).Add(5 * time.Minute)
	for name, basis := range decoded.Bases {
		if !basis.MatchTracked || basis.LatestMatch != nil || !basis.MatchUnknownUntil.Equal(until) {
			t.Fatalf("legacy %+v basis %q: tracked=%t latest=%+v unknownUntil=%s; want no latest and unknown through %s", key, name, basis.MatchTracked, basis.LatestMatch, basis.MatchUnknownUntil, until)
		}
	}
}

// Only the ordinary object/exit summaries retain exact unknown-observation
// times. IP, route, unknown and DNS rows still carry the legacy basis shape.
func assertObservedMatchBounds(t *testing.T, key historystore.TrafficKey, row aggregateBucket, batches [][]config.Observation) {
	t.Helper()
	exact := key.Dimension == "application" || key.Dimension == "system" || key.Dimension == "domain"
	for name, basis := range row.Bases {
		expected := time.UnixMilli(key.BucketMs).Add(5 * time.Minute)
		if exact {
			expected = time.Time{}
			for _, batch := range batches {
				for _, observation := range batch {
					f := observation.Flow
					// In this fixture all non-closing flow observations contribute a start,
					// first failure/response, or nonzero bytes. Closures add no new evidence.
					if f == nil || observation.Kind == "closed" || f.Purpose == "dns" || !f.RoutingMatchedAt.IsZero() {
						continue
					}
					if observation.At.UnixMilli() < key.BucketMs || !observation.At.Before(time.UnixMilli(key.BucketMs).Add(5*time.Minute)) {
						continue
					}
					if f.Rule != basis.Rule || f.RuleSource != basis.Source || f.RouteID != key.Route || f.Direction != key.Direction {
						continue
					}
					object := f.ApplicationID
					switch key.Dimension {
					case "system":
						object = "process:" + f.Process
					case "domain":
						object = config.MainDomain(f.Domain)
						if object == "" && f.IP != "" {
							object = "target:ip-only"
						}
					}
					if object == key.Object && observation.At.After(expected) {
						expected = observation.At
					}
				}
			}
		}
		if !basis.MatchTracked || basis.LatestMatch != nil || expected.IsZero() || !basis.MatchUnknownUntil.Equal(expected) {
			t.Fatalf("fresh %+v basis %q: tracked=%t latest=%+v unknownUntil=%s, want %s", key, name, basis.MatchTracked, basis.LatestMatch, basis.MatchUnknownUntil, expected)
		}
	}
}

func newTrafficRows(t *testing.T, rows []historystore.TrafficRow) map[historystore.TrafficKey]aggregateBucket {
	t.Helper()
	out := map[historystore.TrafficKey]aggregateBucket{}
	for _, r := range rows {
		v, err := decodeTrafficRow(r)
		if err != nil {
			t.Fatal(err)
		}
		out[r.TrafficKey] = v
	}
	return out
}

func TestTrafficWritePathMatchesLegacyAcceptObservations(t *testing.T) {
	h := newTrafficHarness(t)
	batches := trafficEquivalenceFixture(h)
	h.append(trafficEpoch.Add(8*time.Second), true, batches[0]...)
	// A lost range sits between the batches: it changes coverage, never the counters.
	h.appendGap(trafficEpoch.Add(8*time.Second), journalGap{Reason: gapLost, Session: "s", FromSeq: 100, ToSeq: 100})
	h.append(trafficEpoch.Add(16*time.Second), true, batches[1]...)
	h.settle()

	oldRows, oldKinds := readGoldenTraffic(t)
	newRows := newTrafficRows(t, h.rows("traffic_5m"))
	if len(oldRows) != len(newRows) {
		t.Fatalf("row count: old %d new %d", len(oldRows), len(newRows))
	}
	kindRank := func(kinds []string) string {
		best := kinds[0]
		for _, k := range kinds[1:] {
			best = betterSourceKind(best, k)
		}
		return best
	}
	mixed, dimensions := 0, map[string]bool{}
	for key, old := range oldRows {
		lookup := key
		if key.Dimension == "application" && key.Object == "process:/opt/homebrew/bin/curl" {
			lookup.Dimension = "system"
		}
		if key.Dimension == "domain" && key.Object == "unknown" {
			lookup.Object = "target:ip-only"
			old.Identity.Name = "仅记录 IP（域名未识别）"
		}
		got, ok := newRows[lookup]
		if !ok {
			t.Fatalf("row %+v missing from traffic_5m", key)
		}
		dimensions[key.Dimension] = true
		assertLegacyMatchBounds(t, key, *old)
		assertObservedMatchBounds(t, lookup, got, batches)
		if !reflect.DeepEqual(legacyTrafficView(*old), legacyTrafficView(got)) {
			t.Fatalf("row %+v differs:\nold %s\nnew %s", key, pretty(legacyTrafficView(*old)), pretty(legacyTrafficView(got)))
		}
		if got.Identity.Name != old.Identity.Name || got.Identity.RouteName != old.Identity.RouteName || got.Identity.Kind != kindRank(oldKinds[key]) {
			t.Fatalf("row %+v identity: old %+v (kinds %v) new %+v", key, old.Identity, oldKinds[key], got.Identity)
		}
		if len(oldKinds[key]) > 1 {
			mixed++
		} else if got.Identity.Search != old.Identity.Search {
			t.Fatalf("row %+v search: old %q new %q", key, old.Identity.Search, got.Identity.Search)
		}
	}
	for _, d := range []string{"application", "system", "unknown", "dns", "domain", "ip", "route"} {
		if !dimensions[d] {
			t.Fatalf("fixture produced no %s rows (have %v)", d, dimensions)
		}
	}
	if mixed == 0 || len(newRows) < 25 {
		t.Fatalf("fixture too thin: %d rows, %d merged across source kinds", len(newRows), mixed)
	}
	assertFixtureCounters(t, newRows)
	assertRouteSumsMatchLegacy(t, newRows)
	assertRecentMatchesAggregate(t, h, newRows)
}

// assertFixtureCounters pins hand-checked totals so the comparison cannot pass on two equally wrong sides.
func assertFixtureCounters(t *testing.T, rows map[historystore.TrafficKey]aggregateBucket) {
	t.Helper()
	var conns, failed, responses, dnsConns uint64
	for k, v := range rows {
		if k.Dimension == "domain" && k.Object == "example.com" || k.Dimension == "domain" && k.Object == "apple.com" || k.Dimension == "domain" && k.Object == "example.net" || k.Dimension == "domain" && k.Object == "target:ip-only" {
			conns += v.Connections
			failed += v.Failed
			responses += histogramCount(v.Latency)
		}
		if k.Dimension == "dns" {
			dnsConns += v.Connections
			if histogramCount(v.Latency) != 1 {
				t.Fatalf("DNS answered %d times, want once", histogramCount(v.Latency))
			}
		}
	}
	// 7 flows started (the DNS flow is counted only under the dns dimension): appA, sys, lost, rejected, tool, stranger.
	if conns != 6 || dnsConns != 1 || failed != 2 || responses != 3 {
		t.Fatalf("connections %d dns %d failed %d responses %d", conns, dnsConns, failed, responses)
	}
}

// assertRouteSumsMatchLegacy compares the route dimension with the legacy route-buckets, byte for byte,
// including the unattributed object and without DNS.
func assertRouteSumsMatchLegacy(t *testing.T, rows map[historystore.TrafficKey]aggregateBucket) {
	t.Helper()
	type key struct {
		at int64
		id string
	}
	old := map[key][2]uint64{}
	var buckets []stats.Bucket
	readHistoryGolden(t, "traffic-write/routes.json", &buckets)
	for _, b := range buckets {
		old[key{b.At.UnixMilli(), b.RouteID}] = [2]uint64{b.Upload, b.Download}
	}

	got := map[key][2]uint64{}
	for k, v := range rows {
		if k.Dimension == "route" {
			s := got[key{k.BucketMs, k.Object}]
			got[key{k.BucketMs, k.Object}] = [2]uint64{s[0] + v.Upload, s[1] + v.Download}
		}
	}
	for k, v := range got {
		if v == [2]uint64{} {
			delete(got, k) // a route row with only counters
		}
	}
	if !reflect.DeepEqual(old, got) {
		t.Fatalf("route sums differ:\nold %v\nnew %v", old, got)
	}
	unattributed := false
	for k := range got {
		unattributed = unattributed || k.id == "unattributed"
	}
	if !unattributed || len(old) < 3 {
		t.Fatalf("fixture exercised no unattributed route bytes: %v", old)
	}
}

// assertRecentMatchesAggregate checks that recent_cuts holds the same totals at original observation timestamps.
func assertRecentMatchesAggregate(t *testing.T, h *trafficHarness, rows map[historystore.TrafficKey]aggregateBucket) {
	t.Helper()
	sum := map[historystore.TrafficKey]*aggregateBucket{}
	buckets := map[int64]bool{}
	for _, r := range h.rows("recent_cuts") {
		v, err := decodeTrafficRow(r)
		if err != nil {
			t.Fatal(err)
		}
		if r.BucketMs%1000 != 0 {
			t.Fatalf("recent bucket %d is not on the fixture second boundary", r.BucketMs)
		}
		// Unlike a five-minute total, each recent cut identifies the exact input
		// observation. Legacy-shaped dimensions retain their conservative +5m bound.
		expectedUnknown := time.UnixMilli(r.BucketMs)
		switch r.Dimension {
		case "application", "system", "domain":
		default:
			expectedUnknown = expectedUnknown.Add(5 * time.Minute)
		}
		for name, basis := range v.Bases {
			if !basis.MatchTracked || basis.LatestMatch != nil || !basis.MatchUnknownUntil.Equal(expectedUnknown) {
				t.Fatalf("recent cut %+v basis %q: tracked=%t latest=%+v unknownUntil=%s, want %s", r.TrafficKey, name, basis.MatchTracked, basis.LatestMatch, basis.MatchUnknownUntil, expectedUnknown)
			}
		}
		buckets[r.BucketMs] = true
		key := r.TrafficKey
		key.BucketMs -= key.BucketMs % int64(trafficBucket5m/time.Millisecond)
		if sum[key] == nil {
			sum[key] = &aggregateBucket{}
		}
		mergeAggregate(sum[key], v)
	}
	if len(sum) != len(rows) || len(buckets) < 3 {
		t.Fatalf("recent_cuts: %d keys over %d buckets, traffic_5m has %d keys", len(sum), len(buckets), len(rows))
	}
	for key, v := range rows {
		got := sum[key]
		if got == nil {
			t.Fatalf("recent_cuts lacks %+v", key)
		}
		want, have := viewOf(v), viewOf(*got)
		switch key.Dimension {
		case "application", "system", "domain": // Exact metadata must survive aggregation unchanged.
		default:
			// The fallback horizon follows each source row's bucket, so merged cuts
			// cannot equal the coarse total's horizon. Both were asserted independently.
			want, have = legacyTrafficView(v), legacyTrafficView(*got)
		}
		want.Latest, want.LatestAtMs, have.Latest, have.LatestAtMs = 0, 0, 0, 0 // latest is a per-bucket witness
		if !reflect.DeepEqual(want, have) {
			t.Fatalf("recent_cuts %+v differs from traffic_5m:\nwant %s\nhave %s", key, pretty(want), pretty(have))
		}
	}
}
