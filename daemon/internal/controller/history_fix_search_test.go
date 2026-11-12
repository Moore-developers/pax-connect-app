package controller

import (
	"context"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
	"testing"
	"testing/synctest"
	"time"
)

func TestHistoryFixSearchSavedRuleSource(t *testing.T) {
	queryGoldenTimezone(t)
	synctest.Test(t, func(t *testing.T) {
		h := newTrafficHarness(t)
		now := time.Now()
		h.clock = now
		c := New("test", "test")
		c.traffic = h.tr
		at := now.Add(-time.Minute)
		f := config.FlowFact{Key: "rule", StartedAt: at, ApplicationID: "app", Application: "App", Domain: "one.example.test", Direction: "proxy", RouteID: "r", Rule: "domain=one.example.test", RuleSource: "private-fixture-package"}
		h.seq = 1
		h.append(now, true, config.Observation{Session: "s", Sequence: 1, Kind: "started", At: at, Flow: &f, Upload: 10})
		h.settle()
		for _, w := range []string{"today", "week", "month"} {
			p, e := c.QueryTraffic(context.Background(), &pb.TrafficQuery{Window: w, Dimension: "domain", Until: timestamppb.New(now), Search: "private-fixture-package"})
			if e != nil {
				t.Fatal(e)
			}
			if len(p.Items) != 1 || p.Upload != 10 {
				t.Errorf("%s package name absent: rows=%d up=%d", w, len(p.Items), p.Upload)
			}
		}
	})
}

func TestHistoryFixOldSearchEvidenceSurvivesFirstIndexedAppend(t *testing.T) {
	h := newTrafficHarness(t)
	ctx := context.Background()
	at := trafficEpoch.Truncate(5 * time.Minute)
	key := historystore.TrafficKey{BucketMs: at.UnixMilli(), Dimension: "domain", Object: "example.test", Route: "r", Direction: "proxy"}
	if err := h.store.Update(ctx, func(tx *historystore.TrafficTx) error {
		return tx.PutTraffic5m(ctx, historystore.TrafficRow{TrafficKey: key, SourceKind: "app", Search: "old-first\nold-second", UpBytes: 10})
	}); err != nil {
		t.Fatal(err)
	}
	f := trafficFlow("s/new", func(f *config.FlowFact) {
		f.ApplicationID = "new.app"
		f.Application = "New"
		f.Domain = "new.example.test"
		f.RouteID = "r"
	})
	h.append(h.clock, true, h.obs(time.Second, "started", f, 20, 0))
	h.settle()
	h.restart()
	read, err := h.store.BeginRead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Close() }()
	for _, term := range []string{"old-first", "old-second", "new.example.test"} {
		n := 0
		if err = read.MatchingSearch(ctx, 0, at, h.clock, []string{"domain"}, term, func(historystore.TrafficKey) error { n++; return nil }); err != nil || n != 1 {
			t.Fatalf("upgrade lost %q: %d %v", term, n, err)
		}
	}
}

func TestHistoryFixIndexedUnknownSearchCannotMatchOtherDirection(t *testing.T) {
	queryGoldenTimezone(t)
	synctest.Test(t, func(t *testing.T) {
		h := newTrafficHarness(t)
		now := time.Now()
		h.clock = now
		c := New("test", "test")
		c.traffic = h.tr
		for i, direction := range []string{"direct", "proxy"} {
			f := config.FlowFact{Key: direction, StartedAt: now.Add(-time.Minute), Direction: direction, RouteID: direction, Domain: direction + ".example.test"}
			h.seq++
			h.append(now, true, config.Observation{Session: "s", Sequence: h.seq, Kind: "started", At: now.Add(-time.Duration(i+4) * time.Minute), Flow: &f, Upload: 10})
		}
		h.settle()
		for _, window := range []string{"today", "week", "month"} {
			page, err := c.QueryTraffic(context.Background(), &pb.TrafficQuery{Dimension: "application", Window: window, Direction: "proxy", Search: "direct.example.test"})
			if err != nil || page.UnknownSource != nil {
				t.Fatalf("%s indexed direct evidence matched proxy summary: %v", window, err)
			}
		}
	})
}
