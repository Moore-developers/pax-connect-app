package controller

import (
	"context"
	"errors"
	"sort"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

func (r *historyRead) columnTrafficFilter(id string) historystore.ColumnTrafficFilter {
	parts := strings.Split(r.aggregateMemberKeys[id], "\x00")
	f := historystore.ColumnTrafficFilter{From: r.from, Until: r.at, DetailAt: r.detailAt, Direction: r.aggregateDirection}
	if len(parts) >= 4 {
		f.Dimension, f.Object, f.Route, f.MemberDirection = parts[0], parts[1], parts[2], parts[3]
	}
	f.AnyExit = r.groupApplications && f.Dimension == "application"
	if buckets, ok := r.ruleMemberBuckets[id]; ok {
		f.Buckets = []int64{}
		for at := range buckets {
			f.Buckets = append(f.Buckets, at)
		}
		sort.Slice(f.Buckets, func(i, j int) bool { return f.Buckets[i] < f.Buckets[j] })
	}
	return f
}

func columnTrafficFlow(v config.LogConnectionSummary) *pb.FlowRecord {
	f := flowProto(v.Fact)
	f.Upload, f.Download = v.Metrics.Upload, v.Metrics.Download
	return f
}

func (r *historyRead) columnRecent(ctx context.Context, id, group string) ([]*pb.TrafficResponsePoint, error) {
	f := r.columnTrafficFilter(id)
	f.DNSGroupID = group
	return r.columnRecentFilter(ctx, f)
}
func (r *historyRead) columnRecentFilter(ctx context.Context, f historystore.ColumnTrafficFilter) ([]*pb.TrafficResponsePoint, error) {
	values, err := r.columnSnapshot.TrafficRecent(ctx, f)
	if err != nil {
		return nil, err
	}
	var points []*pb.TrafficResponsePoint
	for _, v := range values {
		points = append(points, &pb.TrafficResponsePoint{ConnectionKey: v.Key, StartedAt: timestamppb.New(v.At), Milliseconds: v.Milliseconds})
	}
	return points, nil
}

func (r *historyRead) columnTrafficOutputRow(ctx context.Context, row *pb.TrafficRow) (*pb.TrafficRow, error) {
	if row == nil || r.columnSnapshot == nil {
		return row, nil
	}
	copy := proto.Clone(row).(*pb.TrafficRow)
	points, err := r.columnRecent(ctx, row.Id, "")
	if err != nil {
		if err.Error() == "history_cursor_expired" || ctx.Err() != nil {
			return nil, err
		}
		copy.RecentResponses = nil
		copy.RecentResponsesState, copy.RecentResponsesError = "failed", trafficLogEvidenceError(err)
		return copy, nil
	}
	if len(points) > 20 {
		points = points[len(points)-20:]
	}
	copy.RecentResponses = points
	copy.RecentResponsesState, copy.RecentResponsesError = r.recentResponsesState, r.recentResponsesError
	return copy, nil
}

func trafficLogEvidenceError(err error) string {
	if err != nil && (err.Error() == "history_building" || err.Error() == "history_converting") {
		return err.Error()
	}
	return "history_store_failed"
}

func (r *historyRead) columnConnectionPage(ctx context.Context, q *pb.TrafficDetailRequest, out *pb.TrafficDetail) error {
	if out.Summary.Kind == "dns" {
		return r.columnDNSDetail(ctx, q, out)
	}
	f := r.columnTrafficFilter(q.RowId)
	total, err := r.columnSnapshot.TrafficConnectionCount(ctx, f)
	if err != nil {
		return err
	}
	sig := querySignature([]string{q.RowId, q.ConnectionKey, q.DnsGroupId})
	if !r.aggregate {
		sig = querySignature([]string{q.RowId, q.ConnectionKey})
	}
	start, end, err := historyBoundPage(q.Cursor, total, trafficDetailLimit(q.Limit), 200, sig)
	if err != nil {
		return err
	}
	page, err := r.columnSnapshot.TrafficConnections(ctx, f, start, max(1, end-start))
	if err != nil {
		return err
	}
	out.Items = nil
	for _, v := range page.Items {
		out.Items = append(out.Items, columnTrafficFlow(v))
	}
	out.RetainedConnectionCount = uint64(page.Total)
	out.NextCursor = historyBoundNext(end, page.Total, sig)
	out.RecentResponses, err = r.columnRecent(ctx, q.RowId, "")
	out.RecentResponsesState, out.RecentResponsesError = r.recentResponsesState, r.recentResponsesError
	return err
}

func (r *historyRead) columnPeriodDetail(ctx context.Context, token string, q *pb.TrafficDetailRequest) (*pb.TrafficDetail, error) {
	var row *pb.TrafficRow
	rows := append([]*pb.TrafficRow{}, r.rows...)
	if r.unknown != nil {
		rows = append(rows, r.unknown)
	}
	if r.dns != nil {
		rows = append(rows, r.dns)
	}
	for _, v := range rows {
		if v.Id == q.RowId {
			row = v
			break
		}
	}
	if row == nil {
		return nil, errors.New("history_expired")
	}
	summary, err := r.columnTrafficOutputRow(ctx, row)
	if err != nil {
		return nil, err
	}
	out := &pb.TrafficDetail{Summary: summary, ReadToken: token, AsOf: timestamppb.New(r.at), From: timestamppb.New(r.from), History: historyState(r.meta), Window: r.window, Successful: row.ValidLatencyCount, Failed: row.FailedCount, Unmeasurable: row.MissingLatencyCount, Earlier: row.EarlierCount}
	if err = r.columnConnectionPage(ctx, q, out); err != nil {
		return nil, err
	}
	if row.Kind == "dns" {
		out.Window = "" // dnsTrafficDetail's existing non-aggregate response shape
		return out, nil
	}
	f := r.columnTrafficFilter(row.Id)
	bases := map[string]*pb.TrafficBasis{}
	related := map[string]*pb.TrafficRelated{}
	err = r.columnSnapshot.EachTrafficDetailFact(ctx, f, func(v historystore.ColumnTrafficDetailFact) error {
		key := v.Rule + "\x00" + v.Source + "\x00" + v.Direction + "\x00" + v.Route
		b := bases[key]
		if b == nil {
			b = &pb.TrafficBasis{Rule: v.Rule, Source: v.Source, Target: v.Trigger, RouteName: v.RouteName}
			if r.groupApplications {
				b.Direction = v.Direction
			}
			bases[key] = b
		}
		if b.Target != v.Trigger {
			b.Target = "多个目标，见连接明细"
		}
		b.Connections++
		key, name := v.ApplicationID, v.Application
		if r.dimension == "application" {
			key, name = v.Domain, v.Domain
		}
		if key == "" {
			key, name = "unknown", "未识别"
		}
		x := related[key]
		if x == nil {
			x = &pb.TrafficRelated{Id: key, Name: name}
			related[key] = x
		}
		x.ConnectionKeys = append(x.ConnectionKeys, v.Key)
		x.Download += v.Download
		x.Connections++
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, b := range bases {
		out.Basis = append(out.Basis, b)
	}
	sort.Slice(out.Basis, func(i, j int) bool {
		a, b := out.Basis[i], out.Basis[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		return a.RouteName < b.RouteName
	})
	for _, x := range related {
		out.Related = append(out.Related, x)
	}
	sort.Slice(out.Related, func(i, j int) bool {
		if out.Related[i].Download == out.Related[j].Download {
			return out.Related[i].Id < out.Related[j].Id
		}
		return out.Related[i].Download > out.Related[j].Download
	})
	timings, err := r.columnSnapshot.TrafficTimings(ctx, f)
	if err != nil {
		return nil, err
	}
	out.TimingsTruncated = len(timings) > 256
	for _, v := range timings[:min(len(timings), 256)] {
		out.Timings = append(out.Timings, columnTrafficFlow(v))
	}
	return out, nil
}

func (r *historyRead) columnDNSDetail(ctx context.Context, q *pb.TrafficDetailRequest, out *pb.TrafficDetail) error {
	f := r.columnTrafficFilter(q.RowId)
	f.DNSGroupID = q.DnsGroupId
	sig := querySignature([]string{q.RowId, q.DnsGroupId, "dns-groups-v1"})
	start, end, err := historyBoundPage(q.Cursor, int(^uint32(0)>>1), int(q.Limit), 100, sig)
	if err != nil {
		return err
	}
	page, err := r.columnSnapshot.TrafficDNSPage(ctx, f, start, max(1, end-start))
	if err != nil {
		return err
	}
	count := page.GroupTotal
	if q.DnsGroupId != "" {
		count = page.ConnectionTotal
		if count == 0 {
			return errors.New("history_expired")
		}
	}
	_, end, err = historyBoundPage(q.Cursor, count, int(q.Limit), 100, sig)
	if err != nil {
		return err
	}
	out.DnsGroupId = q.DnsGroupId
	out.DnsGroupTotal = uint64(page.GroupTotal)
	out.NextCursor = historyBoundNext(end, count, sig)
	for _, v := range page.Items {
		out.Items = append(out.Items, columnTrafficFlow(v))
	}
	for _, g := range page.Groups {
		fact := g.Latest.Fact
		ms, known := config.FlowLatency(fact)
		flow := flowProto(fact)
		out.DnsGroups = append(out.DnsGroups, &pb.DNSTrafficGroup{Id: g.ID, Domain: flow.Domain, QueryType: flow.GetDns().GetQueryType(), Result: dnsTrafficResult(flow), Queries: g.Queries, LastAt: flow.StartedAt, Answers: append([]string(nil), flow.GetDns().GetAnswers()...), LatencyMs: ms, LatencyKnown: known})
	}
	out.RecentResponses = nil
	for _, v := range page.Recent {
		ms, _ := config.FlowLatency(v.Fact)
		out.RecentResponses = append(out.RecentResponses, &pb.TrafficResponsePoint{ConnectionKey: v.Fact.Key, StartedAt: timestamppb.New(v.Fact.StartedAt), Milliseconds: ms})
	}
	out.RecentResponsesState, out.RecentResponsesError = r.recentResponsesState, r.recentResponsesError
	return nil
}
