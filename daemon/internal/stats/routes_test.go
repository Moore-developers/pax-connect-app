package stats

import (
	"pax-connect/daemon/internal/config"
	"testing"
	"time"
)

func TestUsageDuplicateAndSwitchPreserveOldRouteAttribution(t *testing.T) {
	now := time.Now()
	d := New()
	tags := map[string]string{"a": "A", "b": "B"}
	a := config.UsageSnapshot{Session: "s", Sequence: 1, At: now, Counters: []config.UsageCounter{{Tag: "a", Upload: 10, Download: 20}}}
	d.Accept(a, tags, now)
	d.Accept(a, tags, now)
	a.Sequence = 2
	a.At = now.Add(time.Second)
	a.Counters = append(a.Counters, config.UsageCounter{Tag: "b", Upload: 5, Download: 7})
	a.Counters[0].Upload = 12
	d.Accept(a, tags, now)
	up, down, _ := d.Today("A", now)
	if up != 12 || down != 20 {
		t.Fatal(up, down)
	}
	up, down, _ = d.Today("B", now)
	if up != 5 || down != 7 {
		t.Fatal(up, down)
	}
}

func TestUsageGapDoesNotPoisonLaterBuckets(t *testing.T) {
	now := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	d := New()
	tags := map[string]string{"a": "A"}
	v := config.UsageSnapshot{Session: "s", Sequence: 1, At: now, Counters: []config.UsageCounter{{Tag: "a", Upload: 10}}}
	d.Accept(v, tags, now)
	// Continuous interval crosses the five-minute boundary.
	d.Last = now.Add(5*time.Minute - time.Second)
	v.Sequence++
	v.At = now.Add(5 * time.Minute)
	v.Counters[0].Upload = 20
	d.Accept(v, tags, v.At)
	if len(d.Buckets) != 2 || d.Buckets[1].Gap || !d.Partial {
		t.Fatal(d)
	}
	v.Sequence++
	v.At = now
	v.Counters[0].Upload = 25
	d.Accept(v, tags, now)
	up, _, partial := d.Today("A", now)
	if up != 25 || !partial {
		t.Fatal(up, partial)
	}
}
func TestUsageRebuildsTodayAfterTimezoneChange(t *testing.T) {
	old := time.Local
	defer func() { time.Local = old }()
	time.Local = time.FixedZone("old", 0)
	now := time.Date(2026, 10, 17, 23, 0, 0, 0, time.UTC)
	d := New()
	d.Accept(config.UsageSnapshot{Session: "s", Sequence: 1, At: now, Counters: []config.UsageCounter{{Tag: "a", Upload: 13}}}, map[string]string{"a": "A"}, now)
	time.Local = time.FixedZone("new", 8*3600)
	up, _, partial := d.Today("A", now)
	if up != 13 || !partial {
		t.Fatal("new local day must be rebuilt from UTC buckets", up, partial)
	}
}
