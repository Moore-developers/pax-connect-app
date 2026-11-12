package controller

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

func TestTrafficSearchFindsMergedObjectWithoutChangingItsCounters(t *testing.T) {
	queryGoldenTimezone(t)
	for _, offset := range []time.Duration{0, 5 * time.Minute} {
		t.Run(offset.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newTrafficHarness(t)
				now := time.Now()
				h.clock = now
				c := New("test", "test")
				c.traffic = h.tr
				first := now.Add(-15 * time.Minute).Truncate(5 * time.Minute)
				app := config.FlowFact{Key: "search/app", StartedAt: first, ApplicationID: "chrome.app", Application: "Chrome", Domain: "one.example.test", RouteID: "r", RouteName: "Line", Direction: "proxy"}
				other := config.FlowFact{Key: "search/unknown", StartedAt: first, Domain: "two.example.test", RouteID: "r", RouteName: "Line", Direction: "proxy"}

				h.seq = 2
				h.append(now, true, config.Observation{Session: "s", Sequence: 1, At: first, Kind: "started", Flow: &app, Upload: 10, Download: 100}, config.Observation{Session: "s", Sequence: 2, At: first.Add(offset + time.Second), Kind: "started", Flow: &other, Upload: 20, Download: 200})
				h.settle()
				expectedRows := 1
				for _, window := range []string{"today", "week", "month"} {
					q := &pb.TrafficQuery{Dimension: "domain", Window: window, Until: timestamppb.New(now)}
					full, err := c.QueryTraffic(context.Background(), q)
					if err != nil {
						t.Fatal(err)
					}
					if len(full.Items) != expectedRows || full.Upload != 30 || full.Download != 300 {
						t.Fatalf("incomplete fixture: rows=%d up=%d down=%d", len(full.Items), full.Upload, full.Download)
					}
					q.Search = "Chrome"
					filtered, err := c.QueryTraffic(context.Background(), q)
					if err != nil {
						t.Fatal(err)
					}
					if len(filtered.Items) != len(full.Items) || filtered.Upload != 30 || filtered.Download != 300 || filtered.Connections != 2 {
						t.Fatalf("%s search must retain whole object: rows=%d up=%d down=%d conns=%d", window, len(filtered.Items), filtered.Upload, filtered.Download, filtered.Connections)
					}
					for i := range full.Items {
						if !proto.Equal(full.Items[i], filtered.Items[i]) {
							t.Fatalf("%s search changed complete row", window)
						}
					}
					if len(full.Trend) != len(filtered.Trend) {
						t.Fatal("search changed trend length")
					}
					for i := range full.Trend {
						if !proto.Equal(full.Trend[i], filtered.Trend[i]) {
							t.Fatalf("search changed trend point %d", i)
						}
					}
				}
			})
		})
	}
}

func TestTrafficSearchAllWindowsKeepCompleteRowsAndDetails(t *testing.T) {
	queryGoldenTimezone(t)
	synctest.Test(t, func(t *testing.T) {
		h := newTrafficHarness(t)
		logs := newLogsHarness(t)
		now := time.Now()
		h.clock = now
		logs.clock = now
		c := New("test", "test")
		c.traffic = h.tr
		c.logs = logs.l
		facts := []config.FlowFact{
			{Key: "rows/chrome", ApplicationID: "chrome.app", Application: "Chrome", Domain: "one.example.test", RouteID: "r", RouteName: "Line", Direction: "proxy"},
			{Key: "rows/other", ApplicationID: "other.app", Application: "Other", Domain: "two.example.test", RouteID: "r", RouteName: "Line", Direction: "proxy"},
			{Key: "rows/unrelated", Domain: "cold.other.test", RouteID: "r", RouteName: "Line", Direction: "proxy"},
			{Key: "rows/direct", ApplicationID: "chrome.app", Application: "Chrome", Domain: "one.example.test", RouteID: "direct", Direction: "direct"},
		}
		var records []config.Observation
		for i := range facts {
			facts[i].StartedAt = now.Add(-50 * time.Second)
			records = append(records, config.Observation{Session: "s", Sequence: uint64(i + 1), Kind: "started", At: now.Add(time.Duration(i-9) * 5 * time.Second), Flow: &facts[i], Upload: uint64(10 * (i + 1)), Download: uint64(100 * (i + 1))})
		}
		h.seq = 4
		h.append(now, true, records...)
		h.settle()
		logs.appendRows(records)
		logs.settle()
		live := newLiveState()
		live.seeded = true
		live.apply(config.ObservationBatch{Session: "s", Collecting: true, Records: records}, now)
		c.liveView.Store(live)
		for _, window := range []string{"minute", "hour", "day", "today", "week", "month"} {
			q := &pb.TrafficQuery{Dimension: "domain", Direction: "proxy", Window: window}
			full, err := c.QueryTraffic(context.Background(), q)
			if err != nil {
				t.Fatal(window, err)
			}
			var expected *pb.TrafficRow
			for _, row := range full.Items {
				if row.ObjectId == "example.test" {
					expected = row
				}
			}
			if expected == nil {
				t.Fatalf("%s missing fixture row", window)
			}
			q.Search = "cHrOmE"
			filtered, err := c.QueryTraffic(context.Background(), q)
			if err != nil {
				t.Fatal(window, err)
			}
			if len(filtered.Items) != 1 || filtered.Upload != 30 || filtered.Download != 300 || filtered.Connections != 2 {
				t.Fatalf("%s search incomplete: rows=%d up=%d down=%d count=%d", window, len(filtered.Items), filtered.Upload, filtered.Download, filtered.Connections)
			}
			if !proto.Equal(expected, filtered.Items[0]) {
				t.Fatalf("%s search changed row", window)
			}
			var up, down uint64
			for _, point := range filtered.Trend {
				up += point.Upload
				down += point.Download
			}
			if up != 30 || down != 300 {
				t.Fatalf("%s trend lost full object: %d/%d", window, up, down)
			}
			if window == "minute" || window == "hour" || window == "day" || window == "today" {
				detail, err := c.QueryTrafficDetail(context.Background(), &pb.TrafficDetailRequest{ReadToken: filtered.ReadToken, RowId: filtered.Items[0].Id})
				if err != nil {
					t.Fatal(window, err)
				}
				if len(detail.Items) != 2 {
					t.Fatalf("%s search clipped connection members: %d", window, len(detail.Items))
				}
			}
			q.Search = "not-recorded"
			empty, err := c.QueryTraffic(context.Background(), q)
			if err != nil || len(empty.Items) != 0 || empty.Upload != 0 || empty.Download != 0 {
				t.Fatalf("%s no-match search leaked rows: %v", window, err)
			}
		}
	})
}

func TestTrafficSearchSystemSubtotalUsesWholeMatchedObject(t *testing.T) {
	queryGoldenTimezone(t)
	synctest.Test(t, func(t *testing.T) {
		h := newTrafficHarness(t)
		logs := newLogsHarness(t)
		now := time.Now()
		h.clock = now
		logs.clock = now
		c := New("test", "test")
		c.traffic = h.tr
		c.logs = logs.l
		if err := h.store.Update(context.Background(), func(tx *historystore.TrafficTx) error {
			for i, search := range []string{"first-keyword", "other-keyword"} {
				row := historystore.TrafficRow{TrafficKey: historystore.TrafficKey{BucketMs: now.Add(time.Duration(i-3) * 5 * time.Minute).UnixMilli(), Dimension: "system", Object: "service", Route: "r", Direction: "proxy"}, Name: "Service", SourceKind: "system", Search: search, UpBytes: int64((i + 1) * 10), DownBytes: int64((i + 1) * 100), NewConns: 1}
				if err := tx.PutTraffic5m(context.Background(), row); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, window := range []string{"hour", "day", "today", "week", "month"} {
			for _, dimension := range []string{"application", "system"} {
				page, err := c.QueryTraffic(context.Background(), &pb.TrafficQuery{Dimension: dimension, Direction: "proxy", Window: window, Search: "first-keyword"})
				if err != nil {
					t.Fatal(window, dimension, err)
				}
				if dimension == "application" {
					if page.SystemUpload != 30 || page.SystemDownload != 300 || page.Upload != 0 || len(page.Items) != 0 {
						t.Fatalf("%s system subtotal clipped/mixed: %d/%d", window, page.SystemUpload, page.SystemDownload)
					}
				} else {
					if page.Upload != 30 || page.Download != 300 || len(page.Items) != 1 {
						t.Fatalf("%s system row clipped: %d/%d", window, page.Upload, page.Download)
					}
				}
			}
		}
	})
}

func TestTrafficSearchEvidenceSurvivesCommitAndRestart(t *testing.T) {
	h := newTrafficHarness(t)
	f := trafficFlow("s/persist/1", func(f *config.FlowFact) {
		f.ApplicationID = "a.app"
		f.Application = "First"
		f.Domain = "one.example.test"
	})
	h.append(h.clock, true, h.obs(time.Second, "started", f, 10, 100))
	h.settle()
	h.restart()
	f = trafficFlow("s/persist/2", func(f *config.FlowFact) {
		f.ApplicationID = "b.app"
		f.Application = "Second"
		f.Domain = "two.example.test"
	})
	h.append(h.clock, true, h.obs(2*time.Second, "started", f, 20, 200))
	h.settle()
	h.restart()
	read, err := h.store.BeginRead(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Close() }()
	for _, term := range []string{"First", "Second"} {
		count := 0
		err := read.MatchingSearch(context.Background(), 0, trafficEpoch.Add(-time.Hour), h.clock, []string{"domain"}, term, func(historystore.TrafficKey) error { count++; return nil })
		if err != nil || count != 1 {
			t.Fatalf("restart lost search term %q: %d %v", term, count, err)
		}
	}
	for _, row := range h.rows("traffic_5m") {
		if row.Dimension == "domain" && (row.UpBytes != 30 || row.DownBytes != 300) {
			t.Fatal("index changed bytes")
		}
	}
}
