package controller

import (
	"context"
	"pax-connect/daemon/internal/stats"

	"google.golang.org/protobuf/proto"
	pb "pax-connect/daemon/gen/proto/paxv1"
)

func aggregateConnectionPage(ctx context.Context, r *historyRead, q *pb.TrafficDetailRequest, out *pb.TrafficDetail) error {
	members := r.aggregateFlowRows[q.RowId]
	count := len(members)
	var modelMembers []*logModelConnection
	if r.logVersion != nil {
		modelMembers = r.selectedConnections(q.RowId)
		count = len(modelMembers)
	}
	signature := querySignature([]string{q.RowId, q.ConnectionKey, q.DnsGroupId})
	start, end := 0, count
	var err error
	// DNS owns a separate group/subgroup cursor contract and validates it below.
	if out.Summary.Kind != "dns" {
		start, end, err = historyBoundPage(q.Cursor, count, trafficDetailLimit(q.Limit), 200, signature)
		if err != nil {
			return err
		}
	}
	first, last := start, end
	var flows []*pb.FlowRecord
	for index := first; index < last; index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		var f stats.FlowWindow
		if r.logVersion != nil {
			at := r.detailAt
			if at.IsZero() {
				at = r.at
			}
			f = r.logVersion.flow(modelMembers[index], at)
		} else {
			f = members[index]
		}
		item := flowProto(f.Fact)
		item.Upload, item.Download = f.Upload, f.Download
		flows = append(flows, item)
	}
	if out.Summary.Kind == "dns" {
		snapshot := *r
		snapshot.dns = out.Summary
		snapshot.flows = map[string][]*pb.FlowRecord{q.RowId: flows}
		detail, err := dnsTrafficDetail(ctx, &snapshot, out.ReadToken, q)
		if err != nil {
			return err
		}
		out.Items, out.DnsGroups = detail.Items, detail.DnsGroups
		out.RecentResponses, out.RecentResponsesState, out.RecentResponsesError = detail.RecentResponses, detail.RecentResponsesState, detail.RecentResponsesError
		out.DnsGroupId, out.DnsGroupTotal, out.NextCursor = detail.DnsGroupId, detail.DnsGroupTotal, detail.NextCursor
		return nil
	}
	out.RetainedConnectionCount = uint64(count)
	out.Items = flows
	out.NextCursor = historyBoundNext(end, count, signature)
	// Returned messages must not let a caller mutate another page's frozen facts.
	for i, item := range out.Items {
		out.Items[i] = proto.Clone(item).(*pb.FlowRecord)
	}
	return nil
}

func trafficDetailLimit(limit uint32) int {
	if limit == 0 {
		return 20
	}
	return int(limit)
}
