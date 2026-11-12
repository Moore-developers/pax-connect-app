package controller

import (
	"testing"
	"time"

	"pax-connect/daemon/internal/historystore"
)

func TestRuleGroupLegacyMetricsAreNeverInvented(t *testing.T) {
	at := time.Unix(1700000000, 0).Truncate(trafficBucket5m)
	v := aggregateBucket{At: at, Identity: aggregateIdentity{Dimension: "domain", Object: "example.test", Route: "direct", Direction: "direct"}, Upload: 40, Download: 90, Connections: 3,
		Bases: map[string]aggregateBasis{
			"old": {Rule: "final", Connections: 1, Observations: 1},
			"new": {Rule: "ip_is_private", Connections: 2, Observations: 2, Contribution: &ruleContribution{Upload: 20, Download: 60, Connections: 2}},
		}}
	row, err := encodeTrafficRow(historystore.TrafficKey{BucketMs: at.UnixMilli(), Dimension: "domain", Object: "example.test", Route: "direct", Direction: "direct"}, v)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := decodeTrafficRow(row)
	if err != nil {
		t.Fatal(err)
	}
	groups := splitRuleBucket(restored)
	if len(groups) != 1 || groups[0].Upload != 40 || groups[0].Connections != 3 {
		t.Fatal("partially-known legacy metrics were split or lost")
	}
	// A single proven package owns the complete historical total even without contributions.
	delete(restored.Bases, "old")
	groups = splitRuleBucket(restored)
	if len(groups) != 1 || groups[0].Upload != 40 {
		t.Fatal("single-package history lost original totals")
	}
	// New complete contributions round-trip and preserve exact per-package bytes/counters.
	v.Bases["old"] = aggregateBasis{Rule: "final", Connections: 1, Observations: 1, Contribution: &ruleContribution{Upload: 20, Download: 30, Connections: 1}}
	row, err = encodeTrafficRow(row.TrafficKey, v)
	if err != nil {
		t.Fatal(err)
	}
	restored, err = decodeTrafficRow(row)
	if err != nil {
		t.Fatal(err)
	}
	groups = splitRuleBucket(restored)
	var up, down, count uint64
	for _, g := range groups {
		up += g.Upload
		down += g.Download
		count += g.Connections
	}
	if len(groups) != 1 || up != 40 || down != 90 || count != 3 {
		t.Fatal("new rule contributions do not conserve stored totals")
	}
}
