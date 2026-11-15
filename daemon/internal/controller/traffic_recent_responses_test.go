package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

func TestRecentResponsesAreExactFrozenTailAcrossWindows(t *testing.T) {
	c, _ := newTrafficRPCFixture(t)
	ctx := context.Background()
	now := time.Now().Add(-10 * time.Second).Truncate(time.Second)
	records := make([]config.Observation, 0, 720)
	for i := 0; i < 720; i++ {
		// Paired timestamps intentionally represent two different connections.
		at := now.Add(-time.Duration(360-i/2) * 50 * time.Millisecond)
		f := config.FlowFact{Key: fmt.Sprintf("recent/1/%04d", i), StartedAt: at, ApplicationID: "recent", Application: "Recent", Purpose: "access", Domain: "recent.test", Direction: "direct", RouteID: "direct", EstablishmentState: "ready", EstablishmentMS: 9999}
		if i%2 == 0 {
			f.ResponseKnown = true
			f.ResponseMS = uint64(i)
		}
		if i == 718 {
			f.ResponseMS = 0
		}
		records = append(records, config.Observation{Session: "recent", Sequence: uint64(i + 1), Kind: "started", At: at.Add(time.Millisecond), Flow: &f})
	}
	if err := FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "recent", HighWater: 720, Collecting: true, Records: records}); err != nil {
		t.Fatal(err)
	}
	for _, window := range []string{"minute", "hour", "day", "today", "week", "month"} {
		t.Run(window, func(t *testing.T) {
			page, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: window})
			if err != nil || len(page.Items) != 1 {
				t.Fatalf("page %v %v", page, err)
			}
			row := page.Items[0]
			detail, err := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: page.ReadToken, RowId: row.Id, Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			if len(row.RecentResponses) != 20 || len(detail.RecentResponses) != 60 {
				t.Fatalf("point count %d/%d", len(row.RecentResponses), len(detail.RecentResponses))
			}
			if row.Connections != 720 || row.ValidLatencyCount != 360 || !proto.Equal(row, detail.Summary) {
				t.Fatalf("summary changed connections=%d valid=%d equal=%v", row.Connections, row.ValidLatencyCount, proto.Equal(row, detail.Summary))
			}
			for i, point := range detail.RecentResponses {
				want := fmt.Sprintf("recent/1/%04d", 600+i*2)
				if point.ConnectionKey != want {
					t.Fatalf("point %d=%s want %s", i, point.ConnectionKey, want)
				}
				if i >= 40 && !proto.Equal(point, row.RecentResponses[i-40]) {
					t.Fatal("list/detail drift")
				}
			}
			if detail.RecentResponses[59].Milliseconds != 0 {
				t.Fatal("zero latency lost")
			}
			again, err := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: page.ReadToken, RowId: row.Id, Limit: 5})
			if err != nil || len(again.RecentResponses) != 60 || !proto.Equal(again.RecentResponses[0], detail.RecentResponses[0]) {
				t.Fatal("detail page selected a different series", err)
			}
		})
	}
}

func TestRecentResponseRevisionOnlyForChangedResponseFacts(t *testing.T) {
	c, _ := newLogsTestController(t, newLogsTestDir(t))
	l := stopWriterGoroutine(t, c)
	ctx := context.Background()
	f := config.FlowFact{Key: "revision/1", StartedAt: time.Now().Add(-time.Minute), Domain: "revision.test", Direction: "direct", RouteID: "direct"}
	apply := func(seq uint64, kind string, upload uint64) {
		t.Helper()
		state := l.state
		var err error
		observation := config.Observation{Session: "revision", Sequence: seq, Kind: kind, At: time.Now(), Flow: &f, Upload: upload}
		body, err := json.Marshal(observation)
		if err != nil {
			t.Fatal(err)
		}
		err = l.logs.Update(ctx, func(tx *historystore.LogsTx) error {
			state, err = l.apply(ctx, tx, state, []historystore.Observation{{Session: "revision", Seq: seq, Kind: kind, Body: body}}, nil, false)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		l.state = state
	}
	apply(1, "started", 0)
	initial := l.state.revision
	f.ResponseKnown = true
	f.ResponseMS = 0
	apply(2, "response", 0)
	if l.state.revision != initial+1 {
		t.Fatal("response-only change did not advance")
	}
	apply(3, "sample", 10)
	if l.state.revision != initial+1 {
		t.Fatal("bytes-only sample moved revision")
	}
	f.ResponseMS = 50
	apply(4, "response", 0)
	if l.state.revision != initial+2 {
		t.Fatal("changed real response did not advance")
	}
	f.Domain = "changed.test"
	apply(5, "sample", 0)
	if l.state.revision != initial+2 {
		t.Fatal("a later sample rewrote the frozen connection identity")
	}
}

func TestRecentResponseTokenExpiresWithLogGenerations(t *testing.T) {
	for _, field := range []string{"delete", "summary", "journal"} {
		t.Run(field, func(t *testing.T) {
			cache := newReadTokenCache()
			cache.observeLogGenerations(1, "summary", "journal")
			r := &historyRead{kind: "traffic", at: time.Now(), logEvidence: true, deletionGeneration: 1, summaryGeneration: "summary", logJournalGeneration: "journal"}
			token, err := cache.save(r)
			if err != nil {
				t.Fatal(err)
			}
			deletion, summary, journal := uint64(1), "summary", "journal"
			switch field {
			case "delete":
				deletion++
			case "summary":
				summary = "next"
			case "journal":
				journal = "next"
			}
			cache.observeLogGenerations(deletion, summary, journal)
			if _, err = cache.take(token, "traffic"); err == nil {
				t.Fatal("old retained facts survived")
			}
			if _, err = cache.save(r); err == nil {
				t.Fatal("in-flight old retained facts reentered cache")
			}
		})
	}
}

func TestRecentResponsesKeepSimultaneousKeys(t *testing.T) {
	r := &historyRead{from: time.Now().Add(-time.Minute), at: time.Now()}
	at := r.at.Add(-time.Second)
	for _, key := range []string{"c", "a", "b"} {
		r.addRecentFact("row", config.FlowFact{Key: key, StartedAt: at, ResponseKnown: true})
	}
	points := r.recentResponses["row"]
	if len(points) != 3 || points[0].ConnectionKey != "a" || points[2].ConnectionKey != "c" {
		t.Fatal(points)
	}
}
