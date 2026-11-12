package controller

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

func calendarWindow(window string, at time.Time) (time.Time, time.Duration, error) {
	local := at.In(time.Local)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	switch window {
	case "today":
		step := 5 * time.Minute
		if at.Sub(start) > 3*time.Hour {
			step = 10 * time.Minute
		}
		if at.Sub(start) > 12*time.Hour {
			step = 15 * time.Minute
		}
		return start, step, nil
	case "yesterday":
		return start.AddDate(0, 0, -1), 15 * time.Minute, nil
	case "hour":
		return at.Add(-time.Hour), 5 * time.Minute, nil
	case "day":
		return at.Add(-24 * time.Hour), 5 * time.Minute, nil
	case "week":
		return start.AddDate(0, 0, -6), 2 * time.Hour, nil
	case "month":
		return start.AddDate(0, 0, -29), 6 * time.Hour, nil
	default:
		return time.Time{}, 0, errors.New("invalid_filter")
	}
}
func periodBin(at, from time.Time, step time.Duration) time.Time {
	return from.Add(time.Duration(at.Sub(from)/step) * step)
}

type aggregateProjection struct {
	row       *pb.TrafficRow
	histogram map[int]uint64
	bins      map[int64]*aggregateBucket
	bases     map[string]aggregateBasis
}

func mergeAggregate(to *aggregateBucket, from aggregateBucket) {
	to.Upload += from.Upload
	to.Download += from.Download
	to.Connections += from.Connections
	to.Failed += from.Failed
	mergeAggregateBases(to, from.Bases)
	if to.Latency == nil {
		to.Latency = map[int]uint64{}
	}
	mergeHistogram(to.Latency, from.Latency)
	to.Maximum = max(to.Maximum, from.Maximum)
	if !from.LatestAt.Before(to.LatestAt) {
		to.Latest = from.Latest
		to.LatestAt = from.LatestAt
	}
}
func (c *Controller) aggregateTrafficRead(ctx context.Context, q *pb.TrafficQuery) (*historyRead, string, error) {
	signature := querySignature([]string{q.Dimension, q.Direction, q.Window, q.Search, q.Until.String()})
	if q.ReadToken != "" {
		r, e := c.readTokens.read(q.ReadToken, "traffic", signature)
		return r, q.ReadToken, e
	}
	if q.Cursor != "" {
		return nil, "", errors.New("history_cursor_expired")
	}
	if !validDimension(q.Dimension) && q.Dimension != "system" && q.Dimension != "network" {
		return nil, "", errors.New("invalid_filter")
	}
	if len(q.Search) > 200 {
		return nil, "", errors.New("invalid_filter")
	}
	if q.Direction != "" && q.Direction != "all" && q.Direction != "proxy" && q.Direction != "direct" {
		return nil, "", errors.New("invalid_filter")
	}
	at := c.now()
	if q.Until != nil {
		at = q.Until.AsTime()
		if at.Before(time.Now().Add(-10*time.Minute)) || at.After(time.Now().Add(time.Second)) {
			return nil, "", errors.New("invalid_filter")
		}
	}
	detailAt := at
	if q.Window == "yesterday" {
		local := at.In(time.Local)
		at = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	}
	from, step, e := calendarWindow(q.Window, detailAt)
	if e != nil {
		return nil, "", e
	}
	r := &historyRead{detailAt: detailAt, at: at, from: from, kind: "traffic", signature: signature, dimension: q.Dimension, meta: c.publishedTrafficMeta(), aggregate: true, window: q.Window, aggregateTrend: map[string][]*pb.TrafficPoint{}}
	groups := map[string]*aggregateProjection{}
	network := map[int64]*aggregateBucket{}
	search := newTrafficSearch(q.Search)
	systemTotals := map[trafficSearchKey]aggregateBucket{}
	unknownPendingMatch := false
	err := func() error {
		snap, e := c.trafficSnapshot(ctx)
		if e != nil {
			return e
		}
		defer func() { _ = snap.read.Close() }()
		unknownPendingMatch = snap.compact.Partial
		r.meta = snap.meta
		r.meta.Partial = r.meta.Partial || snap.compact.Partial
		r.aggregateCoverage = snap.coverage
		r.aggregateCoverageRows, e = snap.coverageRows(ctx, from, at)
		if e != nil {
			return e
		}
		if q.Dimension != "network" {
			if e = c.freezeTrafficConnections(ctx, r, ""); e != nil {
				r.recentResponsesState, r.recentResponsesError = "failed", "history_store_failed"
			}
		}
		if from.Before(r.aggregateCoverage) || !fullAggregateCoverage(r.aggregateCoverageRows, from, at) {
			r.meta.Partial = true
		}
		if err := snap.searchMatches(ctx, from, at, trafficDimensions(q.Dimension), q.Direction, search); err != nil {
			return err
		}
		return snap.eachRuleBucket(ctx, from, at, trafficDimensions(q.Dimension), func(v aggregateBucket) error {
			if e := ctx.Err(); e != nil {
				return e
			}

			if v.Upload == 0 && v.Download == 0 && v.Connections == 0 && v.Failed == 0 && histogramCount(v.Latency) == 0 {
				return nil
			}
			i := v.Identity
			i.Name = storedAggregateName(i)
			if q.Direction != "" && q.Direction != "all" && i.Direction != q.Direction && i.Dimension != "dns" {
				return nil
			}
			search.observeBucket(v)
			if q.Dimension == "application" && i.Dimension == "system" {
				key := searchKey(i)
				sum := systemTotals[key]
				sum.Identity = i
				sum.Upload += v.Upload
				sum.Download += v.Download
				systemTotals[key] = sum
			}
			match := i.Dimension == q.Dimension || q.Dimension == "application" && i.Dimension == "unknown" || q.Dimension == "system" && i.Dimension == "dns" || q.Dimension == "network" && i.Dimension == "route"
			if !match {
				return nil
			}
			id := aggregateRowID(i)
			r.addRuleMemberBucket(id, v)
			g := groups[id]
			kind, label, evidence := i.Kind, i.Name, ""
			if i.Dimension == "domain" || i.Dimension == "ip" {
				kind, label, evidence = "mixed", "", "目标视角包含多种来源"
			}
			if strings.HasPrefix(i.Object, "process:") && (i.Kind == "app" || strings.HasSuffix(i.Object, "/AuthenticationServicesAgent")) {
				label, evidence = storedSource(i).Label, storedSource(i).Evidence
			}
			if g == nil {
				g = &aggregateProjection{row: &pb.TrafficRow{Id: id, ObjectId: i.Object, Name: i.Name, Kind: "access", BytesKnown: i.Kind != "dns", RouteId: i.Route, RouteName: i.RouteName, Direction: i.Direction, SourceKind: kind, SourceLabel: label, SourceEvidence: evidence, LatencyAggregated: true}, histogram: map[int]uint64{}, bins: map[int64]*aggregateBucket{}}
				groups[id] = g
			}
			if mergedRowDimension(i.Dimension) && (i.Dimension == "dns" || g.row.RouteId != i.Route || g.row.Direction != i.Direction) {
				// One summary row spans every route and direction: same shape as the live projection.
				g.row.RouteId, g.row.RouteName = "", ""
				g.row.Direction = "unknown"
				if i.Dimension == "unknown" {
					g.row.Direction, g.row.RouteName = "mixed", "多个连接去向，见详情"
				}
			}
			if v.Upload != 0 || v.Download != 0 || v.Connections != 0 {
				if end := min(v.At.Add(5*time.Minute).UnixMilli(), at.UnixMilli()); end > g.row.LastActiveAt {
					g.row.LastActiveAt = end
				}
			}
			if summarizesTrafficDimension(i.Dimension) {
				addTrafficExit(g.row, i.Route, i.RouteName, i.Direction, v.Upload, v.Download, v.Connections)
			}
			g.bases = mergeRoutingBases(g.bases, v.Bases)
			g.row.Upload += v.Upload
			g.row.Download += v.Download
			g.row.Connections += v.Connections
			g.row.FailedCount += v.Failed
			g.row.SlowestLatencyMs = max(g.row.SlowestLatencyMs, v.Maximum)
			mergeHistogram(g.histogram, v.Latency)
			if !v.LatestAt.IsZero() && (g.row.LatestLatencyAt == nil || !v.LatestAt.Before(g.row.LatestLatencyAt.AsTime())) {
				g.row.LatestLatencyAt = timestamppb.New(v.LatestAt)
				g.row.LatestLatencyMs = v.Latest
			}
			bin := periodBin(v.At, from, step).Unix()
			if g.bins[bin] == nil {
				g.bins[bin] = &aggregateBucket{At: time.Unix(bin, 0)}
				r.bytes += 512 + len(v.Latency)*64
			}
			mergeAggregate(g.bins[bin], v)
			if r.bytes > 24<<20 {
				return errors.New("history_query_busy")
			}
			return nil
		})
	}()
	if err != nil {
		return nil, "", err
	}
	for _, sum := range systemTotals {
		if search.includes(sum.Identity) {
			r.systemUpload += sum.Upload
			r.systemDownload += sum.Download
		}
	}
	// Coverage is shared by every object in a frozen read. Compute each chart
	// interval once instead of rescanning its seconds for every displayed row.
	partialBins := map[int64]bool{}
	partialAt := func(cursor time.Time) bool {
		key := cursor.Unix()
		if partial, ok := partialBins[key]; ok {
			return partial
		}
		partial := !fullAggregateCoverage(r.aggregateCoverageRows, cursor, minTime(cursor.Add(step), at))
		partialBins[key] = partial
		return partial
	}
	for _, g := range groups {
		row := g.row
		if summarizesTrafficDimension(q.Dimension) && row.Kind != "dns" && row.Kind != "unknown" {
			finishTrafficExits(row)
		}
		if !search.includesRow(row, q.Dimension) {
			continue
		}
		if q.Dimension != "application" || row.SourceKind != "unknown" && row.SourceKind != "tool" {
			for bin, v := range g.bins {
				if network[bin] == nil {
					network[bin] = &aggregateBucket{At: v.At}
				}
				mergeAggregate(network[bin], *v)
			}
		}
		row.ValidLatencyCount = histogramCount(g.histogram)
		row.LatencyKnown = row.ValidLatencyCount > 0
		row.MissingLatencyCount = row.Connections - min(row.Connections, row.ValidLatencyCount)
		row.P50Ms = histogramQuantile(g.histogram, 50, row.SlowestLatencyMs)
		row.P95Ms = histogramQuantile(g.histogram, 95, row.SlowestLatencyMs)
		applyAggregateBases(row, g.bases)
		if summarizesTrafficDimension(q.Dimension) && row.Kind != "dns" && row.Kind != "unknown" {
			if !unknownPendingMatch {
				row.LatestMatch = latestRoutingMatch(g.bases, from, at)
			}
		}
		for cursor := from; cursor.Before(at); cursor = cursor.Add(step) {
			if len(row.LatencySamples) >= 128 {
				break
			}
			v := g.bins[cursor.Unix()]
			known := v != nil && histogramCount(v.Latency) > 0
			sample := &pb.TrafficLatencySample{At: timestamppb.New(cursor), Known: known, Seconds: uint32(min(step, at.Sub(cursor)) / time.Second)}
			if known {
				sample.Count = histogramCount(v.Latency)
				sample.Milliseconds = v.Latest
				sample.P95Ms = histogramQuantile(v.Latency, 95, v.Maximum)
			}
			row.LatencySamples = append(row.LatencySamples, sample)
			r.aggregateTrend[row.Id] = append(r.aggregateTrend[row.Id], aggregatePoint(cursor, step, at, v, partialAt(cursor)))
		}
		if row.SourceKind == "dns" {
			row.Kind = "dns"
			r.dns = row
		} else if row.SourceKind == "unknown" && q.Dimension == "application" {
			row.Kind = "unknown"
			r.unknown = row
		} else {
			if row.SourceKind == "system" {
				row.Kind = "system"
			}
			r.rows = append(r.rows, row)
			if q.Dimension == "application" && row.SourceKind == "tool" {
				r.otherProcessUpload += row.Upload
				r.otherProcessDownload += row.Download
				r.otherProcessConnections += row.Connections
			} else {
				r.upload += row.Upload
				r.download += row.Download
				r.connections += row.Connections
			}
		}
		r.bytes += proto.Size(row) + len(row.LatencySamples)*128
		if r.bytes > 24<<20 {
			return nil, "", errors.New("history_query_busy")
		}
	}
	sortTrafficRows(r.rows)
	for cursor := from; cursor.Before(at) && len(r.trend) < 128; cursor = cursor.Add(step) {
		r.trend = append(r.trend, aggregatePoint(cursor, step, at, network[cursor.Unix()], partialAt(cursor)))
	}
	if q.Dimension == "network" {
		r.rows = nil
	}
	if q.Dimension != "network" {
		rows := append([]*pb.TrafficRow(nil), r.rows...)
		if r.unknown != nil {
			rows = append(rows, r.unknown)
		}
		if r.dns != nil {
			rows = append(rows, r.dns)
		}
		if err := indexAggregateConnections(ctx, r, q, rows); err != nil {
			return nil, "", err
		}
	}
	token, err := c.readTokens.save(r)
	return r, token, err
}

// Unknown-source and DNS summaries are single rows regardless of route or direction,
// so both directions of one dimension merge instead of overwriting each other.
func mergedRowDimension(dimension string) bool {
	return dimension == "unknown" || dimension == "dns"
}
func aggregateRowID(i aggregateIdentity) string {
	if i.Dimension == "domain" || i.Dimension == "ip" {
		i.Kind = "mixed"
	}
	if mergedRowDimension(i.Dimension) {
		return aggregateID(aggregateIdentity{Dimension: i.Dimension, Object: i.Object, Kind: i.Kind})
	}
	return aggregateID(i)
}
func aggregatePoint(at time.Time, step time.Duration, until time.Time, v *aggregateBucket, partial bool) *pb.TrafficPoint {
	point := &pb.TrafficPoint{At: timestamppb.New(at), Seconds: uint32(min(step, until.Sub(at)) / time.Second), Gap: v == nil && partial}
	if v != nil {
		point.Upload = v.Upload
		point.Download = v.Download
		point.Gap = false
		point.Partial = partial
	}
	return point
}
func aggregateDetail(ctx context.Context, r *historyRead, token string, q *pb.TrafficDetailRequest) (*pb.TrafficDetail, error) {
	rows := append([]*pb.TrafficRow(nil), r.rows...)
	if r.unknown != nil {
		rows = append(rows, r.unknown)
	}
	if r.dns != nil {
		rows = append(rows, r.dns)
	}
	for _, row := range rows {
		if row.Id == q.RowId {
			out := &pb.TrafficDetail{Summary: r.trafficOutputRow(row), Basis: row.Bases, ReadToken: token, AsOf: timestamppb.New(r.at), From: timestamppb.New(r.from), History: aggregateHistoryState(r), AggregateOnly: true, Window: r.window, Trend: r.aggregateTrend[row.Id], Successful: row.ValidLatencyCount, Failed: row.FailedCount, Unmeasurable: row.MissingLatencyCount}
			r.setRecentDetail(out)
			return out, aggregateConnectionPage(ctx, r, q, out)
		}
	}
	return nil, errors.New("history_expired")
}
func aggregateHistoryState(r *historyRead) *pb.HistoryState {
	v := historyState(r.meta)
	if !r.aggregateCoverage.IsZero() {
		v.AggregateCoverageStart = timestamppb.New(r.aggregateCoverage)
	}
	return v
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// Bucket names are written once and kept for presentation. Executable names are
// derived from the path identity, so buckets saved before the naming rule changed
// (for example a bare version directory) are renamed when read; the ID is untouched.
func storedAggregateName(i aggregateIdentity) string {
	if i.Kind == "tool" && strings.HasPrefix(i.Object, "process:") {
		if path := strings.TrimPrefix(i.Object, "process:"); filepath.IsAbs(path) {
			return config.ExecutableDisplayName(path)
		}
	}
	return i.Name
}
