package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// logLoadConnections and logLoadEvents are the history-storage §11-3 load fixture:
// 24 hours of activity with 15,000 connections and 45,000 events (started, ready and
// closed per connection). buildLogLoadFixture asserts both counts in logs.db.
const (
	logLoadConnections = 15000
	logLoadEvents      = 3 * logLoadConnections
	logLoadChunk       = 1500
)

// buildLogLoadFixture writes the load fixture through the journal and waits for logs.db to
// catch up, so the benchmark measures the query path only.
func buildLogLoadFixture(b testing.TB) *Controller { return buildLogLoadFixtureWindow(b, 0) }

// buildLogLoadFixtureWindow distributes evidence over span when nonzero; the original
// concentrated benchmark remains unchanged for comparisons with its recorded baseline.
func buildLogLoadFixtureWindow(b testing.TB, span time.Duration) *Controller {
	b.Helper()
	c, p := newLogsTestController(b, newLogsTestDir(b))
	base := time.Now().Add(-time.Minute)
	sequence := uint64(0)
	for start := 0; start < logLoadConnections; start += logLoadChunk {
		end := min(logLoadConnections, start+logLoadChunk)
		batch := config.ObservationBatch{Session: "load", Collecting: true}
		for i := start; i < end; i++ {
			started := base.Add(-time.Duration(i%30) * time.Second)
			if span > 0 {
				started = base.Add(-time.Duration(i) * span / logLoadConnections)
			}
			closed := started.Add(time.Second)
			flow := config.FlowFact{Key: fmt.Sprintf("load/1/%d", i), Domain: fmt.Sprintf("target-%d.example.test", i), ApplicationID: "load.fixture", Application: "受控负载", IP: fmt.Sprintf("192.0.2.%d", i%250+1), Port: 443, Transport: "tcp", RouteID: "direct", RouteName: "直连", Direction: "direct", StartedAt: started, ClosedAt: &closed, EstablishmentState: "ready", EstablishmentMS: 12}
			for _, kind := range []string{"started", "ready", "closed"} {
				sequence++
				observation := config.Observation{Session: batch.Session, Sequence: sequence, At: started, Kind: kind, Flow: &flow}
				if kind == "closed" {
					observation.Upload, observation.Download = 12000, 240000
				}
				batch.Records = append(batch.Records, observation)
			}
		}
		batch.HighWater = sequence
		p.feed(batch)
	}
	page, err := c.QueryLogs(context.Background(), &pb.LogQuery{View: "activity", Limit: 1})
	if err != nil || page.Total != logLoadConnections {
		b.Fatalf("load fixture total=%d err=%v", page.GetTotal(), err)
	}
	var events, connections int
	reader := c.logs.logs.(*historystore.Logs).Reader()
	if err = reader.QueryRow("SELECT (SELECT count(*) FROM events),(SELECT count(*) FROM connections)").Scan(&events, &connections); err != nil {
		b.Fatal(err)
	}
	if events != logLoadEvents || connections != logLoadConnections {
		b.Fatalf("load fixture holds %d events and %d connections, want %d and %d", events, connections, logLoadEvents, logLoadConnections)
	}
	return c
}

// BenchmarkQueryLogsActivityFirstPage measures one cold activity page over the §11-3 fixture.
func BenchmarkQueryLogsActivityFirstPage(b *testing.B) {
	c := buildLogLoadFixture(b)
	ctx := context.Background()
	query := &pb.LogQuery{View: "activity", Limit: 100}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		page, err := c.QueryLogs(ctx, query)
		if err != nil {
			b.Fatal(err)
		}
		if page.Total != logLoadConnections || len(page.Records) != 100 {
			b.Fatalf("page total=%d records=%d", page.Total, len(page.Records))
		}
	}
}
