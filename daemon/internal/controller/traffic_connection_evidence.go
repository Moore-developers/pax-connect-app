package controller

import (
	"fmt"
	"sort"
	"strings"

	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/historystore"
)

func (g *connectionTarget) addEvidence(v historystore.ColumnTrafficDetailFact) {
	if g.sources == nil {
		g.sources = map[string]*pb.TrafficConnectionSource{}
		g.addresses = map[string]*pb.TrafficConnectionAddress{}
		g.results = map[string]*pb.TrafficConnectionResult{}
	}
	key := strings.Join([]string{v.ApplicationID, v.Application, v.Process, v.SourceKind}, "\x00")
	if g.sources[key] == nil {
		g.sources[key] = &pb.TrafficConnectionSource{ApplicationId: v.ApplicationID, Application: v.Application, Process: v.Process, Kind: v.SourceKind}
	}
	g.sources[key].Connections++
	key = fmt.Sprintf("%s\x00%d\x00%s", v.IP, v.Port, v.Transport)
	if g.addresses[key] == nil {
		g.addresses[key] = &pb.TrafficConnectionAddress{Ip: v.IP, Port: v.Port, Transport: v.Transport}
	}
	g.addresses[key].Connections++
	state := "active"
	switch {
	case v.Failure != "":
		state = "failed"
	case v.StateUnknown:
		state = "unknown"
	case v.Closed:
		state = "closed"
	}
	key = state + "\x00" + v.Failure
	if g.results[key] == nil {
		g.results[key] = &pb.TrafficConnectionResult{State: state, Failure: v.Failure}
	}
	g.results[key].Connections++
	if v.LatencyKnown {
		g.value.ValidLatencyCount++
		// Input is newest-first, but the direct/legacy path may not be. Keep the
		// latest 60 by real timestamp/key regardless of input order, bounded per target.
		g.value.RecentResponses = append(g.value.RecentResponses, &pb.TrafficResponsePoint{ConnectionKey: v.Key, StartedAt: timestamppb.New(v.StartedAt), Milliseconds: v.LatencyMS})
		sort.Slice(g.value.RecentResponses, func(i, j int) bool {
			a, b := g.value.RecentResponses[i], g.value.RecentResponses[j]
			if a.StartedAt.AsTime().Equal(b.StartedAt.AsTime()) {
				return a.ConnectionKey < b.ConnectionKey
			}
			return a.StartedAt.AsTime().After(b.StartedAt.AsTime())
		})
		if len(g.value.RecentResponses) > 60 {
			g.value.RecentResponses = g.value.RecentResponses[:60]
		}
	}
}

func (g *connectionTarget) finishEvidence() {
	for _, v := range g.sources {
		g.value.Sources = append(g.value.Sources, v)
	}
	for _, v := range g.addresses {
		g.value.Addresses = append(g.value.Addresses, v)
	}
	for _, v := range g.results {
		g.value.Results = append(g.value.Results, v)
	}
	sort.Slice(g.value.Sources, func(i, j int) bool {
		a, b := g.value.Sources[i], g.value.Sources[j]
		if a.Connections != b.Connections {
			return a.Connections > b.Connections
		}
		return strings.Join([]string{a.ApplicationId, a.Application, a.Process, a.Kind}, "\x00") < strings.Join([]string{b.ApplicationId, b.Application, b.Process, b.Kind}, "\x00")
	})
	sort.Slice(g.value.Addresses, func(i, j int) bool {
		a, b := g.value.Addresses[i], g.value.Addresses[j]
		if a.Connections != b.Connections {
			return a.Connections > b.Connections
		}
		return fmt.Sprintf("%s\x00%d\x00%s", a.Ip, a.Port, a.Transport) < fmt.Sprintf("%s\x00%d\x00%s", b.Ip, b.Port, b.Transport)
	})
	sort.Slice(g.value.Results, func(i, j int) bool {
		a, b := g.value.Results[i], g.value.Results[j]
		if a.Connections != b.Connections {
			return a.Connections > b.Connections
		}
		return a.State+"\x00"+a.Failure < b.State+"\x00"+b.Failure
	})
	sort.Slice(g.value.RecentResponses, func(i, j int) bool {
		a, b := g.value.RecentResponses[i], g.value.RecentResponses[j]
		if a.StartedAt.AsTime().Equal(b.StartedAt.AsTime()) {
			return a.ConnectionKey < b.ConnectionKey
		}
		return a.StartedAt.AsTime().Before(b.StartedAt.AsTime())
	})
}
