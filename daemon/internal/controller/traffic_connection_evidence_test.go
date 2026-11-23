package controller

import (
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/historystore"
)

func TestConnectionEvidenceKeepsLatestActualSamplesAndCompleteCounts(t *testing.T) {
	g := &connectionTarget{value: &pb.TrafficConnectionGroup{}}
	at := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	// Deliberately out of order, so the same result is required of direct and SQL reads.
	for n := 0; n < 75; n++ {
		i := (n * 17) % 75
		g.addEvidence(historystore.ColumnTrafficDetailFact{Key: time.Duration(i).String(), StartedAt: at.Add(time.Duration(i) * time.Second), LatencyKnown: true, LatencyMS: uint64(i), ApplicationID: "fixture.app", Application: "Fixture", Process: "/Applications/Fixture.app/Contents/MacOS/Fixture", IP: "203.0.113.1", Port: 443, Transport: "tcp", Closed: true})
	}
	g.addEvidence(historystore.ColumnTrafficDetailFact{Key: "zero", StartedAt: at.Add(76 * time.Second), LatencyKnown: true, LatencyMS: 0, ApplicationID: "fixture.app", Process: "/opt/homebrew/bin/helper", IP: "2001:db8::1", Port: 8443, Transport: "udp"})
	g.addEvidence(historystore.ColumnTrafficDetailFact{Key: "unknown", StartedAt: at.Add(77 * time.Second), StateUnknown: true})
	g.addEvidence(historystore.ColumnTrafficDetailFact{Key: "failed", StartedAt: at.Add(78 * time.Second), Failure: "connection_timeout"})
	g.finishEvidence()
	if g.value.ValidLatencyCount != 76 || len(g.value.RecentResponses) != 60 {
		t.Fatal("sample count", g.value)
	}
	first, last := g.value.RecentResponses[0], g.value.RecentResponses[59]
	if !first.StartedAt.AsTime().Equal(at.Add(16*time.Second)) || last.ConnectionKey != "zero" || last.Milliseconds != 0 {
		t.Fatal("wrong latest valid samples or lost real zero", first, last)
	}
	for i := 1; i < len(g.value.RecentResponses); i++ {
		if g.value.RecentResponses[i].StartedAt.AsTime().Before(g.value.RecentResponses[i-1].StartedAt.AsTime()) {
			t.Fatal("samples not oldest to newest")
		}
	}
	for _, sum := range []uint64{sumConnectionSources(g.value.Sources), sumConnectionAddresses(g.value.Addresses), sumConnectionResults(g.value.Results)} {
		if sum != 78 {
			t.Fatal("incomplete evidence counts", sum)
		}
	}
	states := map[string]uint64{}
	for _, v := range g.value.Results {
		states[v.State] += v.Connections
	}
	if states["closed"] != 75 || states["active"] != 1 || states["unknown"] != 1 || states["failed"] != 1 {
		t.Fatal("states conflated", states)
	}
}
func sumConnectionSources(values []*pb.TrafficConnectionSource) uint64 {
	var n uint64
	for _, v := range values {
		n += v.Connections
	}
	return n
}
func sumConnectionAddresses(values []*pb.TrafficConnectionAddress) uint64 {
	var n uint64
	for _, v := range values {
		n += v.Connections
	}
	return n
}
func sumConnectionResults(values []*pb.TrafficConnectionResult) uint64 {
	var n uint64
	for _, v := range values {
		n += v.Connections
	}
	return n
}
