package controller

import (
	"context"
	"errors"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"sort"
)

func dnsTrafficGroupID(f *pb.FlowRecord) string {
	return "dns-group:" + querySignature([]string{f.Domain, f.GetDns().GetQueryType(), dnsTrafficResult(f)})
}
func dnsTrafficResult(f *pb.FlowRecord) string {
	if f.GetDns().GetResult() != "" {
		return f.Dns.Result
	}
	if f.Failure != "" {
		return f.Failure
	}
	return "结果未记录"
}

// DNS groups and per-query records are projections of the same frozen members.
// Direction filtering intentionally does not apply to the separate DNS summary.
func dnsTrafficDetail(ctx context.Context, r *historyRead, token string, q *pb.TrafficDetailRequest) (*pb.TrafficDetail, error) {
	row := r.dns
	if row == nil || q.RowId != row.Id {
		return nil, errors.New("history_expired")
	}
	flows := r.flows[row.Id]
	groups := map[string]*pb.DNSTrafficGroup{}
	size := r.bytes
	for _, f := range flows {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		id := dnsTrafficGroupID(f)
		g := groups[id]
		if g == nil {
			g = &pb.DNSTrafficGroup{Id: id, Domain: f.Domain, QueryType: f.GetDns().GetQueryType(), Result: dnsTrafficResult(f)}
			groups[id] = g
			size += len(id) + len(f.Domain) + 512
		}
		g.Queries++
		if g.LastAt == nil || f.StartedAt.AsTime().After(g.LastAt.AsTime()) {
			g.LastAt = f.StartedAt
			g.Answers = append([]string(nil), f.GetDns().GetAnswers()...)
			g.LatencyMs, g.LatencyKnown = trafficProtoLatency(f)
		}
		if size > 32<<20 {
			return nil, errors.New("history_query_busy")
		}
	}
	out := &pb.TrafficDetail{Summary: row, ReadToken: token, AsOf: row.LatestLatencyAt, History: historyState(r.meta), DnsGroupId: q.DnsGroupId, DnsGroupTotal: uint64(len(groups)), Successful: row.ValidLatencyCount, Failed: row.FailedCount, Unmeasurable: row.MissingLatencyCount, Earlier: row.EarlierCount}
	r.setRecentDetail(out)
	// The page time is the snapshot boundary, not the latest successful sample.
	out.AsOf = timestamppb.New(r.at)
	out.From = timestamppb.New(r.from)
	sig := querySignature([]string{row.Id, q.DnsGroupId, "dns-groups-v1"})
	if q.DnsGroupId != "" {
		if groups[q.DnsGroupId] == nil {
			return nil, errors.New("history_expired")
		}
		var members []*pb.FlowRecord
		for _, f := range flows {
			if dnsTrafficGroupID(f) == q.DnsGroupId {
				members = append(members, f)
			}
		}
		start, end, e := historyBoundPage(q.Cursor, len(members), int(q.Limit), 100, sig)
		if e != nil {
			return nil, e
		}
		out.Items = members[start:end]
		selected := *r
		selected.recentResponses = nil
		selected.logVersion = nil // subgroup chart is computed from its own frozen members below
		for _, f := range members {
			if !f.StartedAt.AsTime().After(r.from) || f.StartedAt.AsTime().After(r.at) {
				continue
			}
			if ms, known := trafficProtoLatency(f); known {
				selected.addRecentResponse(row.Id, &pb.TrafficResponsePoint{ConnectionKey: f.Key, StartedAt: f.StartedAt, Milliseconds: ms})
			}
		}
		selected.setRecentDetail(out)
		out.DnsGroups = []*pb.DNSTrafficGroup{groups[q.DnsGroupId]}
		out.NextCursor = historyBoundNext(end, len(members), sig)
	} else {
		var rows []*pb.DNSTrafficGroup
		for _, g := range groups {
			rows = append(rows, g)
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].LastAt.AsTime().Equal(rows[j].LastAt.AsTime()) {
				return rows[i].Id < rows[j].Id
			}
			return rows[i].LastAt.AsTime().After(rows[j].LastAt.AsTime())
		})
		start, end, e := historyBoundPage(q.Cursor, len(rows), int(q.Limit), 100, sig)
		if e != nil {
			return nil, e
		}
		out.DnsGroups = rows[start:end]
		out.NextCursor = historyBoundNext(end, len(rows), sig)
	}
	if proto.Size(out) > 8<<20 {
		return nil, errors.New("history_query_busy")
	}
	return out, nil
}
