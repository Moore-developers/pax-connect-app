package controller

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/stats"
)

// periodTrafficRead keeps retained connection/latency evidence for hour/day consumers,
// while byte totals and charts come from traffic.db's five-minute buckets.
func (c *Controller) periodTrafficRead(ctx context.Context, q *pb.TrafficQuery) (*historyRead, string, error) {
	signature := querySignature([]string{q.Dimension, q.Direction, q.Window, q.Search, q.Until.String()})
	if q.ReadToken != "" {
		r, e := c.readTokens.read(q.ReadToken, "traffic", signature)
		return r, q.ReadToken, e
	}
	at := time.Now().UTC().Truncate(time.Second)
	from, _, err := calendarWindow(q.Window, at)
	if err != nil {
		return nil, "", err
	}
	snap, err := c.trafficSnapshot(ctx)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = snap.read.Close() }()
	r := &historyRead{at: at, from: from, kind: "traffic", signature: signature, dimension: q.Dimension, window: q.Window, meta: snap.meta, flows: map[string][]*pb.FlowRecord{}}
	if err = c.freezeTrafficConnections(ctx, r, ""); err != nil {
		return nil, "", err
	}
	buckets, err := snap.ruleBuckets(ctx, from, at, trafficDimensions(q.Dimension))
	if err != nil {
		return nil, "", err
	}
	values := append([]stats.FlowWindow(nil), r.aggregateFlows...)
	for i := range values {
		values[i].Upload, values[i].Download = 0, 0
	}
	all := proto.Clone(q).(*pb.TrafficQuery)
	all.Search = ""
	err = c.projectTrafficRead(ctx, r, all, values)
	if err != nil {
		return nil, "", err
	}
	type key struct{ object, route, direction string }
	keyFor := func(object, route, direction string) key {
		if mergedRowDimension(q.Dimension) {
			route, direction = "", ""
		}
		return key{object, route, direction}
	}
	exitRows := map[key]*pb.TrafficRow{}
	sums := map[key]aggregateBucket{}
	search := newTrafficSearch(q.Search)
	if err := snap.searchMatches(ctx, from, at, trafficDimensions(q.Dimension), q.Direction, search); err != nil {
		return nil, "", err
	}
	points := map[int64]*pb.TrafficPoint{}
	for _, v := range buckets {
		i := v.Identity
		r.addRuleMemberBucket(aggregateRowID(i), v)
		match := i.Dimension == q.Dimension || q.Dimension == "" && i.Dimension == "domain" || q.Dimension == "application" && i.Dimension == "unknown" || q.Dimension == "system" && i.Dimension == "dns"
		if !match || q.Direction != "" && q.Direction != "all" && i.Direction != q.Direction && i.Dimension != "dns" {
			continue
		}
		search.observeBucket(v)
		k := keyFor(i.Object, i.Route, i.Direction)
		if i.Dimension == "unknown" || i.Dimension == "dns" {
			k.route, k.direction = "", ""
		}
		if summarizesTrafficDimension(i.Dimension) {
			if exitRows[k] == nil {
				exitRows[k] = &pb.TrafficRow{}
			}
			addTrafficExit(exitRows[k], i.Route, i.RouteName, i.Direction, v.Upload, v.Download, v.Connections)
		}
		sum := sums[k]
		sum.Identity = i
		sum.At = v.At
		sum.Connections += v.Connections
		sum.Bases = mergeRoutingBases(sum.Bases, v.Bases)
		sum.Failed += v.Failed
		sum.Upload += v.Upload
		sum.Download += v.Download
		sums[k] = sum
	}
	for _, v := range values {
		for _, id := range aggregateIdentities(v.Fact) {
			if q.Direction == "" || q.Direction == "all" || id.Direction == q.Direction || id.Dimension == "dns" {
				search.observe(id)
			}
		}
	}
	present := map[key]bool{}
	for _, row := range r.rows {
		present[keyFor(row.ObjectId, row.RouteId, row.Direction)] = true
	}
	if r.unknown != nil {
		present[keyFor(r.unknown.ObjectId, "", "")] = true
	}
	if r.dns != nil {
		present[keyFor(r.dns.ObjectId, "", "")] = true
	}
	for k, v := range sums {
		if present[k] {
			continue
		}
		i := v.Identity
		row := &pb.TrafficRow{Id: aggregateRowID(i), ObjectId: i.Object, Name: storedAggregateName(i), RouteId: i.Route, RouteName: i.RouteName, Direction: i.Direction, Basis: i.RulePackage, Connections: v.Connections, FailedCount: v.Failed, Kind: "access", BytesKnown: true, SourceKind: i.Kind, SourceLabel: storedAggregateName(i), LastActiveAt: min(v.At.Add(5*time.Minute).UnixMilli(), at.UnixMilli())}
		r.setRecentRow(row)
		switch i.Dimension {
		case "unknown":
			row.Kind = "unknown"
			r.unknown = row
		case "dns":
			row.Kind, row.BytesKnown = "dns", false
			r.dns = row
		default:
			r.rows = append(r.rows, row)
		}
		r.connections += row.Connections
		r.flows[row.Id] = nil
	}
	visible := r.rows[:0]
	for _, row := range r.rows {
		_, retained := sums[keyFor(row.ObjectId, row.RouteId, row.Direction)]
		if retained && search.includesRow(row, q.Dimension) {
			visible = append(visible, row)
		} else {
			delete(r.flows, row.Id)
		}
	}
	r.rows = visible
	if !search.includesRow(r.unknown, q.Dimension) {
		if r.unknown != nil {
			delete(r.flows, r.unknown.Id)
		}
		r.unknown = nil
	}
	if !search.includesRow(r.dns, q.Dimension) {
		if r.dns != nil {
			delete(r.flows, r.dns.Id)
		}
		r.dns = nil
	}
	rows := append([]*pb.TrafficRow(nil), r.rows...)
	if r.unknown != nil {
		rows = append(rows, r.unknown)
	}
	if r.dns != nil {
		rows = append(rows, r.dns)
	}
	if r.logVersion != nil {
		if err := indexAggregateConnections(ctx, r, q, rows); err != nil {
			return nil, "", err
		}
	}
	r.systemUpload, r.systemDownload = 0, 0
	if q.Dimension == "application" {
		for _, v := range buckets {
			if v.Identity.Dimension == "system" && (q.Direction == "" || q.Direction == "all" || v.Identity.Direction == q.Direction) {
				search.observeBucket(v)
			}
		}
		for _, v := range buckets {
			if v.Identity.Dimension == "system" && (q.Direction == "" || q.Direction == "all" || v.Identity.Direction == q.Direction) {
				if !search.includes(v.Identity) {
					continue
				}
				r.systemUpload += v.Upload
				r.systemDownload += v.Download
			}
		}
	}
	r.upload, r.download, r.otherProcessUpload, r.otherProcessDownload = 0, 0, 0, 0
	for _, row := range rows {
		k := keyFor(row.ObjectId, row.RouteId, row.Direction)
		if row.Kind == "unknown" || row.Kind == "dns" {
			k.route, k.direction = "", ""
		}
		v := sums[k]
		if summarizesTrafficDimension(q.Dimension) && row.Kind != "unknown" && row.Kind != "dns" {
			r.ruleMemberBuckets[row.Id] = r.ruleMemberBuckets[aggregateRowID(v.Identity)]
			if r.logVersion != nil {
				if r.recentResponses == nil {
					r.recentResponses = map[string][]*pb.TrafficResponsePoint{}
				}
				r.recentResponses[row.Id] = r.modelRecentResponses(row.Id)
			} else {
				members := r.flows[row.Id][:0]
				for _, fact := range r.flows[row.Id] {
					if r.ruleMemberAllowed(row.Id, fact.StartedAt.AsTime()) {
						members = append(members, fact)
					}
				}
				r.flows[row.Id] = members
				delete(r.recentResponses, row.Id)
				for _, fact := range members {
					r.addRecentFact(row.Id, config.FlowFact{Key: fact.Key, StartedAt: fact.StartedAt.AsTime(), ResponseKnown: fact.ResponseKnown, ResponseMS: fact.ResponseMs, EstablishmentState: fact.EstablishmentState, EstablishmentMS: fact.EstablishmentMs, Evidence: fact.Evidence})
				}
			}

			r.setRecentRow(row)
		}
		row.Upload, row.Download, row.Connections = v.Upload, v.Download, v.Connections
		if exits := exitRows[k]; exits != nil {
			row.Exits = exits.Exits
			finishTrafficExits(row)
			row.Bases = nil
			applyAggregateBases(row, v.Bases)
			row.LatestMatch = nil
			if !snap.compact.Partial {
				row.LatestMatch = latestRoutingMatch(v.Bases, from, at)
			}
		}
		if q.Dimension == "application" && row.SourceKind == "tool" {
			r.otherProcessUpload += row.Upload
			r.otherProcessDownload += row.Download
		} else if row.Kind != "unknown" && row.Kind != "dns" {
			r.upload += row.Upload
			r.download += row.Download
		}
	}
	// Counts use the same persisted new-connection horizon as byte totals. A
	// retained connection can predate the window or belong to a legacy target
	// bucket, so counting detail members here would mix two populations.
	r.connections, r.otherProcessConnections = 0, 0
	for _, row := range r.rows {
		if q.Dimension == "application" && row.SourceKind == "tool" {
			r.otherProcessConnections += row.Connections
		} else {
			r.connections += row.Connections
		}
	}
	// Build the chart using exactly the visible rows, so filtering never changes its scope.
	allowed := map[key]bool{}
	for _, row := range r.rows {
		if q.Dimension == "application" && row.SourceKind == "tool" {
			continue
		}
		allowed[keyFor(row.ObjectId, row.RouteId, row.Direction)] = true
	}
	for _, v := range buckets {
		if q.Direction != "" && q.Direction != "all" && v.Identity.Direction != q.Direction && v.Identity.Dimension != "dns" {
			continue
		}
		if !allowed[keyFor(v.Identity.Object, v.Identity.Route, v.Identity.Direction)] {
			continue
		}
		bin := v.At.Truncate(5 * time.Minute).Unix()
		p := points[bin]
		if p == nil {
			p = &pb.TrafficPoint{At: timestamppb.New(time.Unix(bin, 0)), Seconds: 300}
			points[bin] = p
		}
		p.Upload += v.Upload
		p.Download += v.Download
	}
	for t := from.Truncate(5 * time.Minute); t.Before(at); t = t.Add(5 * time.Minute) {
		if p := points[t.Unix()]; p != nil {
			r.trend = append(r.trend, p)
		}
	}
	r.recentResponses = nil
	if err := indexAggregateConnections(ctx, r, q, rows); err != nil {
		return nil, "", err
	}
	if r.logVersion == nil {
		for id, members := range r.aggregateFlowRows {
			r.flows[id] = nil
			for _, member := range members {
				item := flowProto(member.Fact)
				item.Upload, item.Download = member.Upload, member.Download
				r.flows[id] = append(r.flows[id], item)
			}
		}
	}
	if r.logVersion != nil {
		r.aggregateFlows = nil
	}
	sortTrafficRows(r.rows)
	token, err := c.readTokens.save(r)
	return r, token, err
}

func trafficDimensions(dimension string) []string {
	switch dimension {
	case "application":
		return []string{"application", "unknown", "system"}
	case "system":
		return []string{"application", "system", "dns"}
	case "network":
		return []string{"route"}
	case "", "domain":
		return []string{"domain"}
	default:
		return []string{dimension}
	}
}
