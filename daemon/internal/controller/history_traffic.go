package controller

import (
	"context"
	"errors"

	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/stats"
	"sort"
	"time"
)

func (c *Controller) trafficRead(ctx context.Context, q *pb.TrafficQuery) (*historyRead, string, error) {
	if (!validDimension(q.Dimension) && q.Dimension != "system" && q.Dimension != "network") || len(q.Search) > 200 || q.Direction != "" && q.Direction != "all" && q.Direction != "direct" && q.Direction != "proxy" {
		return nil, "", errors.New("invalid_filter")
	}
	if q.Cursor != "" && q.ReadToken == "" {
		return nil, "", errors.New("history_cursor_expired")
	}

	if q.Window == "hour" || q.Window == "day" {
		return c.periodTrafficRead(ctx, q)
	}
	if q.Window == "today" || q.Window == "yesterday" || q.Window == "week" || q.Window == "month" {
		return c.aggregateTrafficRead(ctx, q)
	}
	if q.Window != "" && q.Window != "minute" || (!validDimension(q.Dimension) && q.Dimension != "system") || len(q.Search) > 200 || q.Direction != "" && q.Direction != "all" && q.Direction != "proxy" && q.Direction != "direct" {
		return nil, "", errors.New("invalid_filter")
	}
	sig := querySignature([]string{q.Dimension, q.Direction, q.Window, q.Search})
	if q.ReadToken != "" {
		r, e := c.readTokens.read(q.ReadToken, "traffic", sig)
		return r, q.ReadToken, e
	}
	if q.Cursor != "" {
		return nil, "", errors.New("history_cursor_expired")
	}
	live := c.liveView.Load()
	if live == nil {
		return nil, "", errors.New("history_unavailable")
	}
	at := time.Now().UTC().Truncate(5 * time.Second)
	from := at.Add(-time.Minute)
	r := &historyRead{at: at, from: from, kind: "traffic", signature: sig, dimension: q.Dimension, window: q.Window, meta: c.publishedTrafficMeta(), flows: map[string][]*pb.FlowRecord{}}
	var values []stats.FlowWindow
	points := map[int64]*pb.TrafficPoint{}
	windows := map[string][liveWindowPoints]livePoint{}
	for _, v := range live.Flows(at) {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		f := v.Fact
		c.mapHistoryRoute(&f)
		identifyHistoryApplicationContext(ctx, &f)
		if f.StartedAt.After(at) || f.ClosedAt != nil && !f.ClosedAt.After(from) {
			continue
		}
		w := stats.FlowWindow{Fact: f}
		for i, p := range v.Window {
			end := from.Add(time.Duration(i+1) * 5 * time.Second)
			w.Upload += p.Upload
			w.Download += p.Download
			if p.Upload+p.Download > 0 {
				w.LastTransfer = end
			}
		}
		values = append(values, w)
		windows[f.Key] = v.Window
	}
	if err := c.projectTrafficRead(ctx, r, q, values); err != nil {
		return nil, "", err
	}
	for _, row := range r.rows {
		if q.Dimension == "application" && row.SourceKind == "tool" {
			continue
		}
		for _, flow := range r.flows[row.Id] {
			for i, p := range windows[flow.Key] {
				at := from.Add(time.Duration(i) * 5 * time.Second)
				point := points[at.Unix()]
				if point == nil {
					point = &pb.TrafficPoint{At: timestamppb.New(at), Seconds: 5}
					points[at.Unix()] = point
				}
				point.Upload += p.Upload
				point.Download += p.Download
			}
		}
	}

	for t := from; t.Before(at); t = t.Add(5 * time.Second) {
		p := points[t.Unix()]
		if p == nil {
			p = &pb.TrafficPoint{At: timestamppb.New(t), Seconds: 5}
		}
		r.trend = append(r.trend, p)
	}
	token, err := c.readTokens.save(r)
	return r, token, err
}

// QueryTraffic returns complete frozen aggregates and a bounded ranked page.
func (c *Controller) QueryTraffic(ctx context.Context, q *pb.TrafficQuery) (*pb.TrafficPage, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out := &pb.TrafficPage{}
	err := func() error {
		r, token, e := c.trafficRead(ctx, q)
		if e != nil {
			return e
		}
		start, end, e := historyBoundPage(q.Cursor, len(r.rows), int(q.Limit), 100, r.signature)
		if e != nil {
			return e
		}
		items := make([]*pb.TrafficRow, 0, end-start)
		for _, row := range r.rows[start:end] {
			items = append(items, r.trafficOutputRow(row))
		}
		out = &pb.TrafficPage{UnknownSource: r.trafficOutputRow(r.unknown), DnsSummary: r.trafficOutputRow(r.dns), Items: items, ReadToken: token, NextCursor: historyBoundNext(end, len(r.rows), r.signature), AsOf: timestamppb.New(r.at), From: timestamppb.New(r.from), Upload: r.upload, Download: r.download, Connections: r.connections, SystemUpload: r.systemUpload, SystemDownload: r.systemDownload, OtherProcessUpload: r.otherProcessUpload, OtherProcessDownload: r.otherProcessDownload, OtherProcessConnections: r.otherProcessConnections, Trend: r.trend, History: aggregateHistoryState(r), Window: r.window, TotalRows: uint64(len(r.rows))}
		return nil
	}()
	if err != nil {
		return nil, historyError(err)
	}
	if err := c.readTokens.ensureLive(ctx, out.ReadToken); err != nil {
		return nil, historyError(err)
	}
	return out, nil
}

// QueryTrafficDetail keeps summary values fixed while connection pages are loaded.
func (c *Controller) QueryTrafficDetail(ctx context.Context, q *pb.TrafficDetailRequest) (*pb.TrafficDetail, error) {
	if len(q.DnsGroupId) > 128 {
		return nil, historyError(errors.New("invalid_filter"))
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out := &pb.TrafficDetail{}
	err := func() error {
		token := q.ReadToken
		var r *historyRead
		if token != "" {
			var e error
			if r, e = c.readTokens.take(token, "traffic", trafficDetailReadKind); e != nil {
				return e
			}
		} else if q.ConnectionKey != "" {
			var e error
			r, token, e = c.connectionTrafficRead(ctx, q.ConnectionKey)
			if e != nil {
				return e
			}
		}
		if r == nil {
			return errors.New("history_cursor_expired")
		}
		if r.kind != trafficDetailReadKind {
			var err error
			r, token, err = c.readTokens.freezeTrafficDetail(r)
			if err != nil {
				return err
			}
		}
		if r.logVersion != nil && !r.aggregate && r.aggregateConnectionRows != nil {
			local := *r
			local.flows = map[string][]*pb.FlowRecord{}
			ids := []string{q.RowId}
			if q.ConnectionKey != "" {
				ids = nil
				for id := range r.aggregateConnectionRows {
					ids = append(ids, id)
				}
			}
			for _, id := range ids {
				for _, entry := range r.selectedConnections(id) {
					if q.ConnectionKey != "" && entry.summary.Fact.Key != q.ConnectionKey {
						continue
					}
					f := r.logVersion.flow(entry, r.at)
					item := flowProto(f.Fact)
					item.Upload, item.Download = f.Upload, f.Download
					local.flows[id] = append(local.flows[id], item)
				}
			}
			r = &local
		}
		if r.aggregate {
			var err error
			out, err = aggregateDetail(ctx, r, token, q)
			return err
		}
		if r.dns != nil && q.RowId == r.dns.Id && q.ConnectionKey == "" {
			var e error
			out, e = dnsTrafficDetail(ctx, r, token, q)
			return e
		}
		var summary *pb.TrafficRow
		var flows []*pb.FlowRecord
		detailRows := append([]*pb.TrafficRow(nil), r.rows...)
		if r.unknown != nil {
			detailRows = append(detailRows, r.unknown)
		}
		if r.dns != nil {
			detailRows = append(detailRows, r.dns)
		}
		for _, row := range detailRows {
			if err := ctx.Err(); err != nil {
				return err
			}
			if row.Id == q.RowId {
				summary = row
				flows = r.flows[row.Id]
				break
			}
			if q.ConnectionKey != "" {
				for _, f := range r.flows[row.Id] {
					if err := ctx.Err(); err != nil {
						return err
					}
					if f.Key == q.ConnectionKey {
						summary = &pb.TrafficRow{SourceKind: f.SourceKind, SourceLabel: f.SourceLabel, SourceEvidence: f.SourceEvidence, Kind: row.Kind, BytesKnown: row.BytesKnown, Id: f.Key, Name: f.Domain, RouteId: f.RouteId, RouteName: f.RouteName, Direction: f.Direction, Upload: f.Upload, Download: f.Download, Connections: 1, EstablishmentKnown: f.EstablishmentState == "ready" && f.StartedAt.AsTime().After(r.from), SlowestMs: f.EstablishmentMs}
						latency, known := trafficProtoLatency(f)
						summary.LatencyKnown = known && f.StartedAt.AsTime().After(r.from)

						if !f.StartedAt.AsTime().After(r.from) {
							summary.EarlierCount = 1
						} else {
							if known {
								summary.ValidLatencyCount = 1
							} else {
								summary.MissingLatencyCount = 1
							}
							if f.Failure != "" || f.EstablishmentState == "failed" {
								summary.FailedCount = 1
							}
						}
						if summary.LatencyKnown {
							summary.SlowestLatencyMs = latency
							summary.LatestLatencyMs = latency
							summary.LatestLatencyAt = f.StartedAt
							summary.LatencySamples = []*pb.TrafficLatencySample{{At: f.StartedAt, Milliseconds: latency, Known: true}}
							summary.RecentResponses = []*pb.TrafficResponsePoint{{ConnectionKey: f.Key, StartedAt: f.StartedAt, Milliseconds: latency}}
							summary.RecentResponsesState, summary.RecentResponsesError = r.recentResponsesState, r.recentResponsesError
						}
						flows = []*pb.FlowRecord{f}
						break
					}
				}
			}
		}
		if summary == nil {
			return errors.New("history_expired")
		}
		start, end, e := historyBoundPage(q.Cursor, len(flows), trafficDetailLimit(q.Limit), 200, querySignature([]string{q.RowId, q.ConnectionKey}))
		if e != nil {
			return e
		}
		basis := map[string]*pb.TrafficBasis{}
		for _, f := range flows {
			if err := ctx.Err(); err != nil {
				return err
			}
			key := f.Rule + "\x00" + f.RuleSource + "\x00" + f.Direction + "\x00" + f.RouteId
			if basis[key] == nil {
				basis[key] = &pb.TrafficBasis{Rule: f.Rule, Source: f.RuleSource, Target: trafficProtoTrigger(f), RouteName: f.RouteName}
			}
			if basis[key].Target != trafficProtoTrigger(f) {
				basis[key].Target = "多个目标，见连接明细"
			}
			basis[key].Connections++
		}
		var ordered []*pb.TrafficBasis
		for _, v := range basis {
			if err := ctx.Err(); err != nil {
				return err
			}
			ordered = append(ordered, v)
		}
		sort.Slice(ordered, func(i, j int) bool {
			a, b := ordered[i], ordered[j]
			if a.Source == b.Source {
				if a.Rule == b.Rule {
					return a.RouteName < b.RouteName
				}
				return a.Rule < b.Rule
			}
			return a.Source < b.Source
		})
		out = &pb.TrafficDetail{RetainedConnectionCount: uint64(len(flows)), Successful: summary.ValidLatencyCount, Failed: summary.FailedCount, Unmeasurable: summary.MissingLatencyCount, Window: r.window, Earlier: summary.EarlierCount, Summary: summary, Items: flows[start:end], Basis: ordered, ReadToken: token, NextCursor: historyBoundNext(end, len(flows), querySignature([]string{q.RowId, q.ConnectionKey})), AsOf: timestamppb.New(r.at), From: timestamppb.New(r.from), History: historyState(r.meta)}
		r.setRecentDetail(out)
		if q.ConnectionKey != "" {
			out.RecentResponses = cloneRecentResponses(summary.RecentResponses)
		}
		related := map[string]*pb.TrafficRelated{}
		for _, f := range flows {
			if err := ctx.Err(); err != nil {
				return err
			}
			key, name := f.ApplicationId, f.Application
			if r.dimension == "application" {
				key = config.MainDomain(f.Domain)
				name = key
			}
			if key == "" {
				key = "unknown"
				name = "未识别"
			}
			if related[key] == nil {
				related[key] = &pb.TrafficRelated{Id: key, Name: name}
			}
			v := related[key]
			v.ConnectionKeys = append(v.ConnectionKeys, f.Key)
			v.Download += f.Download
			v.Connections++
			if f.StartedAt.AsTime().After(r.from) {
				if _, known := trafficProtoLatency(f); known {
					if len(out.Timings) < 256 {
						out.Timings = append(out.Timings, f)
					} else {
						out.TimingsTruncated = true
					}
				}
			}
		}
		for _, v := range related {
			if err := ctx.Err(); err != nil {
				return err
			}
			out.Related = append(out.Related, v)
		}
		sort.Slice(out.Related, func(i, j int) bool {
			if out.Related[i].Download == out.Related[j].Download {
				return out.Related[i].Id < out.Related[j].Id
			}
			return out.Related[i].Download > out.Related[j].Download
		})

		return nil
	}()
	if err != nil {
		return nil, historyError(err)
	}
	if err := c.readTokens.ensureLive(ctx, out.ReadToken); err != nil {
		return nil, historyError(err)
	}
	return out, nil
}

func trafficProtoTrigger(f *pb.FlowRecord) string {
	return config.TrafficTrigger(config.FlowFact{Application: f.Application, Domain: f.Domain, IP: f.Ip, Rule: f.Rule})
}

func trafficProtoLatency(f *pb.FlowRecord) (uint64, bool) {
	return config.FlowLatency(config.FlowFact{ResponseKnown: f.ResponseKnown, ResponseMS: f.ResponseMs, EstablishmentState: f.EstablishmentState, EstablishmentMS: f.EstablishmentMs, Evidence: f.Evidence})
}
