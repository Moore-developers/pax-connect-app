package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
	"pax-connect/daemon/internal/stats"
)

// columnPeriodRows replaces the all-retained-connection projection for hour/day.
// Traffic buckets still replace bytes/counts/trends in the existing caller.
func (c *Controller) columnPeriodRows(ctx context.Context, r *historyRead, q *pb.TrafficQuery) (map[trafficSearchKey]bool, error) {
	dimension := q.Dimension
	if dimension == "" {
		dimension = "domain"
	}
	dimensions := []string{dimension}
	if dimension == "application" {
		dimensions = append(dimensions, "unknown")
	}
	if dimension == "system" {
		dimensions = append(dimensions, "dns")
	}
	matched := map[trafficSearchKey]bool{}
	for _, dim := range dimensions {
		filter := historystore.ColumnTrafficFilter{Dimension: dim, Direction: q.Direction, From: r.from, Until: r.at, DetailAt: r.detailAt, Search: q.Search}
		groups, err := r.columnSnapshot.TrafficPeriodGroups(ctx, filter)
		if err != nil {
			return nil, err
		}
		for _, g := range groups {
			fact := g.Representative.Fact
			projectionDimension := dim
			if q.Dimension == "" && dim == "domain" {
				projectionDimension = ""
			}
			base := stats.AggregateTraffic([]stats.FlowWindow{{Fact: fact}}, projectionDimension, q.Direction, "", r.from, r.at)
			if len(base) != 1 {
				continue
			}
			source := config.ClassifyTrafficSource(fact)
			row := &pb.TrafficRow{Id: base[0].ID, ObjectId: g.Object, Name: base[0].Name, RouteId: fact.RouteID, RouteName: fact.RouteName, Direction: fact.Direction, Connections: g.Connections, SlowestMs: g.Establishment, EstablishmentKnown: g.EstablishmentKnown, Kind: "access", BytesKnown: true, SourceKind: source.Kind, SourceLabel: source.Label, SourceEvidence: source.Evidence, ValidLatencyCount: g.Valid, MissingLatencyCount: g.Missing, FailedCount: g.Failed, EarlierCount: g.Earlier, SlowestLatencyMs: g.Slowest, LatencyKnown: g.Valid > 0, LatencySampled: g.Valid > 256}
			if !g.LastActive.IsZero() {
				row.LastActiveAt = g.LastActive.UnixMilli()
			}
			if dim == "domain" || dim == "ip" {
				row.SourceKind, row.SourceLabel, row.SourceEvidence = "mixed", "", "目标视角包含多种来源"
			}
			if row.SourceKind == "system" {
				row.Kind = "system"
			}
			if dim == "unknown" {
				row.Kind = "unknown"
				if g.MixedExit {
					row.Direction, row.RouteId, row.RouteName = "mixed", "", "多个连接去向，见详情"
				}
			}
			if dim == "dns" {
				row.Kind, row.BytesKnown, row.RouteId, row.RouteName, row.Direction = "dns", false, "", "", "unknown"
			}
			lines := map[string]bool{}
			for _, b := range g.Bases {
				row.Bases = append(row.Bases, &pb.TrafficBasis{Rule: b.Rule, Source: b.Source, RouteName: b.RouteName, Connections: b.Connections})
				lines[config.TrafficBasisLine(b.Rule, b.Source)] = true
			}
			var names []string
			for line := range lines {
				names = append(names, line)
			}
			sort.Strings(names)
			if len(names) > 2 {
				row.Basis = strings.Join(names[:2], "\n") + fmt.Sprintf("\n另 %d 项", len(names)-2)
			} else {
				row.Basis = strings.Join(names, "\n")
			}
			for _, sample := range g.Latency {
				row.LatencySamples = append(row.LatencySamples, &pb.TrafficLatencySample{At: timestamppb.New(sample.At), Milliseconds: sample.Milliseconds, Known: true})
			}
			if len(row.LatencySamples) > 0 {
				last := row.LatencySamples[len(row.LatencySamples)-1]
				row.LatestLatencyAt, row.LatestLatencyMs = last.At, last.Milliseconds
			}
			if g.MatchesSearch {
				matched[searchKey(aggregateIdentity{Dimension: dim, Object: g.Object, Route: g.Route, Direction: g.Direction})] = true
			}
			if dim == "dns" {
				r.dns = row
			} else if dim == "unknown" && dimension == "application" {
				r.unknown = row
			} else {
				r.rows = append(r.rows, row)
			}
		}
	}
	return matched, nil
}
