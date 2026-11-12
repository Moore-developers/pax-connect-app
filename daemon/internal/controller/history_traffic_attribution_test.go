package controller

import (
	"context"
	"pax-connect/daemon/internal/config"
	"testing"
	"time"
)

func TestTrafficIdentityBeforeCommitAppliesToWholeBatch(t *testing.T) {
	h := newTrafficHarness(t)
	unknown := &config.FlowFact{Key: "s/late", StartedAt: trafficEpoch, Direction: "unknown"}
	known := *unknown
	known.ApplicationID, known.Application, known.Direction, known.RouteID = "fixture.app", "Fixture", "proxy", "route-a"
	h.append(h.clock, true, h.obs(time.Second, "started", unknown, 10, 90), h.obs(2*time.Second, "sample", &known, 100, 900))
	h.settle()
	up, down, starts, _ := h.counters("application")
	if up != 110 || down != 990 || starts != 1 {
		t.Fatalf("known identity totals = %d/%d/%d; want 110/990/1", up, down, starts)
	}
	up, down, starts, _ = h.counters("unknown")
	if up != 0 || down != 0 || starts != 0 {
		t.Fatalf("unidentified contribution survived: %d/%d/%d", up, down, starts)
	}
}

func TestTrafficIdentityAfterCommitKeepsEarlierAttribution(t *testing.T) {
	h := newTrafficHarness(t)
	f := &config.FlowFact{Key: "s/late", StartedAt: trafficEpoch, Direction: "unknown"}
	h.append(h.clock, true, h.obs(time.Second, "started", f, 10, 90))
	h.settle()
	known := *f
	known.ApplicationID, known.Application, known.Direction, known.RouteID = "fixture.app", "Fixture", "proxy", "route-a"
	h.append(h.clock.Add(time.Second), true, h.obs(2*time.Second, "sample", &known, 100, 900))
	h.settle()
	up, down, starts, _ := h.counters("unknown")
	if up != 10 || down != 90 || starts != 1 {
		t.Fatalf("committed unknown changed: %d/%d/%d", up, down, starts)
	}
	up, down, starts, _ = h.counters("application")
	if up != 100 || down != 900 || starts != 0 {
		t.Fatalf("later known changed: %d/%d/%d", up, down, starts)
	}
}

func TestTrafficIdentityBeyondTransactionPageIsResolvedBeforeCommit(t *testing.T) {
	h := newTrafficHarness(t)
	f := &config.FlowFact{Key: "s/late-page", StartedAt: trafficEpoch, Direction: "unknown"}
	records := []config.Observation{h.obs(time.Second, "started", f, 10, 90)}
	for range trafficBatchRecords {
		records = append(records, h.obs(time.Second, "phase", f, 0, 0))
	}
	known := *f
	known.ApplicationID, known.Application = "fixture.app", "Fixture"
	records = append(records, h.obs(2*time.Second, "sample", &known, 100, 900))
	h.append(h.clock, true, records...)
	if delay := h.tr.step(context.Background()); delay != 0 {
		t.Fatalf("first batch delay=%v", delay)
	}
	if up, down, starts, _ := h.counters("application"); up != 10 || down != 90 || starts != 1 {
		t.Fatalf("first page did not use later uncommitted identity: %d/%d/%d", up, down, starts)
	}
	if up, down, starts, _ := h.counters("unknown"); up != 0 || down != 0 || starts != 0 {
		t.Fatalf("first page committed unknown: %d/%d/%d", up, down, starts)
	}
	h.settle()
	up, down, starts, _ := h.counters("application")
	if up != 110 || down != 990 || starts != 1 {
		t.Fatalf("page boundary lost identity: %d/%d/%d", up, down, starts)
	}
}
