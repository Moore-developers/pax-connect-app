package controller

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/stats"
)

const recentResponseLimit = 60

// addRecentResponse keeps actual connection points, never the legacy chart's reduced samples.
func (r *historyRead) addRecentResponse(id string, point *pb.TrafficResponsePoint) {
	if r.recentResponses == nil {
		r.recentResponses = map[string][]*pb.TrafficResponsePoint{}
	}
	points := r.recentResponses[id]
	if points == nil {
		r.bytes += len(id) + 128
	}
	r.bytes += proto.Size(point) + 128
	before := func(a, b *pb.TrafficResponsePoint) bool {
		if a.StartedAt.AsTime().Equal(b.StartedAt.AsTime()) {
			return a.ConnectionKey < b.ConnectionKey
		}
		return a.StartedAt.AsTime().Before(b.StartedAt.AsTime())
	}
	index := sort.Search(len(points), func(i int) bool { return !before(points[i], point) })
	points = append(points, nil)
	copy(points[index+1:], points[index:])
	points[index] = point
	if len(points) > recentResponseLimit {
		r.bytes -= proto.Size(points[0]) + 128
		points[0] = nil
		points = points[1:]
	}
	r.recentResponses[id] = points
}

func (r *historyRead) addRecentFact(id string, f config.FlowFact) {
	if !f.StartedAt.After(r.from) || f.StartedAt.After(r.at) {
		return
	}
	if ms, known := config.FlowLatency(f); known {
		r.addRecentResponse(id, &pb.TrafficResponsePoint{ConnectionKey: f.Key, StartedAt: timestamppb.New(f.StartedAt), Milliseconds: ms})
	}
}

func (r *historyRead) setRecentRow(row *pb.TrafficRow) {
	points := r.recentResponses[row.Id]
	if len(points) > 20 {
		points = points[len(points)-20:]
	}
	row.RecentResponses = cloneRecentResponses(points)
	row.RecentResponsesState, row.RecentResponsesError = r.recentResponsesState, r.recentResponsesError
}

func (r *historyRead) setRecentDetail(out *pb.TrafficDetail) {
	if r.logVersion != nil {
		out.RecentResponses = r.modelRecentResponses(out.Summary.Id)
	} else {
		out.RecentResponses = cloneRecentResponses(r.recentResponses[out.Summary.Id])
	}
	out.RecentResponsesState, out.RecentResponsesError = r.recentResponsesState, r.recentResponsesError
}

func cloneRecentResponses(points []*pb.TrafficResponsePoint) []*pb.TrafficResponsePoint {
	out := make([]*pb.TrafficResponsePoint, len(points))
	for i, point := range points {
		out[i] = proto.Clone(point).(*pb.TrafficResponsePoint)
	}
	return out
}

// indexAggregateConnections groups the frozen retained facts once for both charts and pages.
// Search chooses whole rows; it never removes individual members of a matched object.
func indexAggregateConnections(ctx context.Context, r *historyRead, q *pb.TrafficQuery, rows []*pb.TrafficRow) error {
	allowed := map[string]*pb.TrafficRow{}
	for _, row := range rows {
		dimension := r.dimension
		if dimension == "" {
			dimension = "domain"
		}
		if row.Kind == "dns" {
			dimension = "dns"
		}
		if row.Kind == "unknown" {
			dimension = "unknown"
		}
		allowed[aggregateMemberKey(aggregateIdentity{Dimension: dimension, Object: row.ObjectId, Route: row.RouteId, Direction: row.Direction, RulePackage: row.Basis})] = row
	}
	if r.logVersion != nil {
		// Keep the shared object directory, not a copied per-token member list. Time,
		// direction and read-correction filters are applied by the token's selector.
		r.aggregateConnectionRows = map[string][]*logModelConnection{}
		r.aggregateMemberKeys = map[string]string{}
		r.aggregateDirection = q.Direction
		for key, row := range allowed {
			r.aggregateConnectionRows[row.Id] = r.logVersion.members[key]
			r.aggregateMemberKeys[row.Id] = key
			r.bytes += len(row.Id) + len(key) + 48
		}
		for _, row := range rows {
			r.bytes += proto.Size(row)
			row.RecentResponsesState, row.RecentResponsesError = r.recentResponsesState, r.recentResponsesError
		}
		if r.bytes > 24<<20 {
			return errors.New("history_query_busy")
		}
		return nil
	}
	r.aggregateFlowRows = map[string][]stats.FlowWindow{}
	for _, f := range r.aggregateFlows {
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, identity := range aggregateIdentities(f.Fact) {
			row := allowed[aggregateMemberKey(identity)]
			if row == nil {
				continue
			}
			id := row.Id
			if row.Kind == "dns" && (!f.Fact.StartedAt.After(r.from) || f.Fact.StartedAt.After(r.at)) {
				continue
			}
			if identity.Dimension != "dns" && q.Direction != "" && q.Direction != "all" && identity.Direction != q.Direction {
				continue
			}
			r.aggregateFlowRows[id] = append(r.aggregateFlowRows[id], f)
			r.bytes += 1024 // copied FlowWindow and its slice/map index overhead; fact payload remains shared
			if r.ruleMemberAllowed(id, f.Fact.StartedAt) {
				r.addRecentFact(id, f.Fact)
			}
		}
		if r.bytes > 24<<20 {
			return errors.New("history_query_busy")
		}
	}
	for _, row := range rows {
		r.setRecentRow(row)
		r.bytes += proto.Size(row)
	}
	if r.bytes > 24<<20 {
		return errors.New("history_query_busy")
	}
	return nil
}

func aggregateMemberKey(i aggregateIdentity) string {
	i.RulePackage = ""
	if mergedRowDimension(i.Dimension) {
		i.Route, i.Direction, i.RulePackage = "", "", ""
	}
	return strings.Join([]string{i.Dimension, i.Object, i.Route, i.Direction, i.RulePackage}, "\x00")
}

func (r *historyRead) modelConnectionAllowed(id string, entry *logModelConnection) bool {
	detailAt := r.detailAt
	if detailAt.IsZero() {
		detailAt = r.at
	}
	f := entry.summary.Fact
	cutoff := detailAt.Add(-24 * time.Hour)
	if f.StartedAt.Before(cutoff) || f.StartedAt.After(detailAt) {
		return false
	}
	key := r.aggregateMemberKeys[id]
	dns := strings.HasPrefix(key, "dns\x00")
	if dns && (!f.StartedAt.After(r.from) || f.StartedAt.After(r.at)) {
		return false
	}
	if f.Direction == "unknown" && f.RouteID == "" && f.Failure == "rule_rejected" {
		f = r.logVersion.flow(entry, detailAt).Fact
		matched := false
		for _, identity := range aggregateIdentities(f) {
			if aggregateMemberKey(identity) == key {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return dns || r.aggregateDirection == "" || r.aggregateDirection == "all" || f.Direction == r.aggregateDirection
}
func (r *historyRead) selectedConnections(id string) []*logModelConnection {
	var selected []*logModelConnection
	for _, entry := range r.aggregateConnectionRows[id] {
		if r.modelConnectionAllowed(id, entry) {
			selected = append(selected, entry)
		}
	}
	return selected
}
func (r *historyRead) modelRecentResponses(id string) []*pb.TrafficResponsePoint {
	at := r.detailAt
	if at.IsZero() {
		at = r.at
	}
	entries := r.aggregateConnectionRows[id]
	var points []*pb.TrafficResponsePoint
	for first := 0; first < len(entries) && len(points) < recentResponseLimit; {
		end := first + 1
		for end < len(entries) && entries[first].summary.Fact.StartedAt.Equal(entries[end].summary.Fact.StartedAt) {
			end++
		}
		// Directory order is start DESC/key ASC. The chart's tail needs DESC/key DESC.
		for index := end - 1; index >= first && len(points) < recentResponseLimit; index-- {
			entry := entries[index]
			if !r.modelConnectionAllowed(id, entry) {
				continue
			}
			f := r.logVersion.flow(entry, at).Fact
			if !f.StartedAt.After(r.from) || f.StartedAt.After(r.at) || !r.ruleMemberAllowed(id, f.StartedAt) {
				continue
			}
			if ms, known := config.FlowLatency(f); known {
				points = append(points, &pb.TrafficResponsePoint{ConnectionKey: f.Key, StartedAt: timestamppb.New(f.StartedAt), Milliseconds: ms})
			}
		}
		first = end
	}
	for i, j := 0, len(points)-1; i < j; i, j = i+1, j-1 {
		points[i], points[j] = points[j], points[i]
	}
	return points
}

func (r *historyRead) trafficOutputRow(row *pb.TrafficRow) *pb.TrafficRow {
	if row == nil || r.logVersion == nil {
		return row
	}
	copy := proto.Clone(row).(*pb.TrafficRow)
	points := r.modelRecentResponses(row.Id)
	if len(points) > 20 {
		points = points[len(points)-20:]
	}
	copy.RecentResponses = points
	return copy
}
