package controller

import (
	"context"
	"errors"
	"fmt"
	"google.golang.org/protobuf/proto"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/stats"
	"sort"
	"strings"
)

func (c *Controller) finishTrafficRead(ctx context.Context, r *historyRead, q *pb.TrafficQuery, values []stats.FlowWindow) (*historyRead, string, error) {
	if err := c.projectTrafficRead(ctx, r, q, values); err != nil {
		return nil, "", err
	}
	token, err := c.readTokens.save(r)
	return r, token, err
}

func (c *Controller) projectTrafficRead(ctx context.Context, r *historyRead, q *pb.TrafficQuery, values []stats.FlowWindow) error {
	from, at := r.from, r.at
	if r.recentResponsesState == "" {
		r.recentResponsesState = "ready"
	}
	if q.Dimension == "network" {
		return nil
	}
	r.upload, r.download = 0, 0
	groups := stats.AggregateTraffic(values, q.Dimension, q.Direction, "", from, at)
	if q.Dimension == "application" {
		groups = append(groups, stats.AggregateTraffic(values, "unknown", q.Direction, "", from, at)...)
	}
	if q.Dimension == "system" {
		groups = append(groups, stats.AggregateTraffic(values, "dns", "all", "", from, at)...)
	}
	if q.Dimension == "application" {
		for _, g := range stats.AggregateTraffic(values, "system", q.Direction, "", from, at) {
			if !trafficGroupMatchesSearch(g, q.Search) {
				continue
			}
			r.systemUpload += g.Upload
			r.systemDownload += g.Download
		}
	}
	for _, g := range groups {
		if !trafficGroupMatchesSearch(g, q.Search) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		row := &pb.TrafficRow{Id: g.ID, ObjectId: g.ObjectID, Name: g.Name, RouteId: g.RouteID, RouteName: g.RouteName, Direction: g.Direction, Upload: g.Upload, Download: g.Download, Connections: uint64(len(g.Flows)), SlowestMs: g.Slowest, EstablishmentKnown: g.EstablishmentKnown, Kind: "access", BytesKnown: true}
		if !g.LastActive.IsZero() {
			row.LastActiveAt = g.LastActive.UnixMilli()
		}
		source := config.ClassifyTrafficSource(g.Flows[0].Fact)
		row.SourceKind, row.SourceLabel, row.SourceEvidence = source.Kind, source.Label, source.Evidence
		if q.Dimension == "" || q.Dimension == "domain" || q.Dimension == "ip" {
			source.Kind = "mixed"
			row.SourceKind = "mixed"
			row.SourceLabel = ""
			row.SourceEvidence = "目标视角包含多种来源"
		}
		if source.Kind == "system" {
			row.Kind = "system"
		}
		if source.Kind == "unknown" {
			row.Kind = "unknown"
		}
		if source.Kind == "dns" {
			row.Kind = "dns"
			row.BytesKnown = false
			row.Upload = 0
			row.Download = 0
		}
		bases := map[string]*pb.TrafficBasis{}
		lines := map[string]bool{}
		for _, f := range g.Flows {
			if err := ctx.Err(); err != nil {
				return err
			}
			if summarizesTrafficDimension(q.Dimension) && row.Kind != "dns" && row.Kind != "unknown" {
				addTrafficExit(row, f.Fact.RouteID, f.Fact.RouteName, f.Fact.Direction, f.Upload, f.Download, 1)
			}
			key := f.Fact.Rule + "\x00" + f.Fact.RuleSource
			if bases[key] == nil {
				bases[key] = &pb.TrafficBasis{Rule: f.Fact.Rule, Source: f.Fact.RuleSource, RouteName: f.Fact.RouteName}
			}
			bases[key].Connections++
			lines[config.TrafficBasisLine(f.Fact.Rule, f.Fact.RuleSource)] = true
			if r.logVersion != nil {
				continue
			}
			v := flowProto(f.Fact)
			v.Upload = f.Upload
			v.Download = f.Download
			if row.Kind == "dns" {
				v.Upload = 0
				v.Download = 0
			}
			r.bytes += proto.Size(v)
			if r.bytes > 24<<20 {
				return errors.New("history_query_busy")
			}
			r.flows[g.ID] = append(r.flows[g.ID], v)
		}
		sort.Slice(r.flows[g.ID], func(i, j int) bool {
			a, b := r.flows[g.ID][i], r.flows[g.ID][j]
			if a.StartedAt.AsTime().Equal(b.StartedAt.AsTime()) {
				return a.Key < b.Key
			}
			return a.StartedAt.AsTime().After(b.StartedAt.AsTime())
		})
		for _, f := range g.Flows {
			r.addRecentFact(row.Id, f.Fact)
		}
		if summarizesTrafficDimension(q.Dimension) && row.Kind != "dns" && row.Kind != "unknown" {
			finishTrafficExits(row)
		}
		r.setRecentRow(row)
		if err := fillTrafficLatency(ctx, row, g.Flows, from); err != nil {
			return err
		}
		names := make([]string, 0, len(lines))
		for line := range lines {
			names = append(names, line)
		}
		sort.Strings(names)
		if len(names) > 2 {
			row.Basis = strings.Join(names[:2], "\n") + fmt.Sprintf("\n另 %d 项", len(names)-2)
		} else {
			row.Basis = strings.Join(names, "\n")
		}
		for _, b := range bases {
			row.Bases = append(row.Bases, b)
		}
		sort.Slice(row.Bases, func(i, j int) bool {
			return row.Bases[i].Rule+row.Bases[i].Source < row.Bases[j].Rule+row.Bases[j].Source
		})
		if summarizesTrafficDimension(q.Dimension) && row.Kind != "dns" && row.Kind != "unknown" {
			matchBases := map[string]aggregateBasis{}
			for _, member := range g.Flows {
				f := member.Fact
				b := aggregateBasis{MatchTracked: true, LatestMatch: routingMatch(f)}
				if f.RoutingMatchedAt.IsZero() {
					b.MatchUnknownUntil = at
				}
				matchBases[f.Key] = b
			}
			row.LatestMatch = latestRoutingMatch(matchBases, from, at)
		}
		r.bytes += proto.Size(row)
		if r.bytes > 24<<20 {
			return errors.New("history_query_busy")
		}
		if row.Kind == "dns" {
			r.dns = row
			continue
		}
		if row.Kind == "unknown" && q.Dimension == "application" {
			r.unknown = row
			continue
		}
		r.rows = append(r.rows, row)
		if q.Dimension == "application" && source.Kind == "tool" {
			r.otherProcessUpload += row.Upload
			r.otherProcessDownload += row.Download
			r.otherProcessConnections += row.Connections
		} else {
			r.upload += row.Upload
			r.download += row.Download
			r.connections += row.Connections
		}
	}
	return nil
}
