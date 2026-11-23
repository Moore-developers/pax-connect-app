package controller

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

type connectionTarget struct {
	value             *pb.TrafficConnectionGroup
	rules             map[string]*pb.TrafficBasis
	exits             map[string]*pb.TrafficExit
	known, incomplete bool
	sources           map[string]*pb.TrafficConnectionSource
	addresses         map[string]*pb.TrafficConnectionAddress
	results           map[string]*pb.TrafficConnectionResult
}

func connectionTargetIdentity(host, ip string) (string, string, string) {
	if host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), ".")); host != "" {
		return "domain:" + host, host, "domain"
	}
	if ip != "" {
		return "ip:" + ip, ip, "ip"
	}
	return "unknown", "", "unknown"
}
func connectionRuleIdentity(source, rule string) string {
	return fmt.Sprint(len(source)) + ":" + source + rule
}
func connectionExitIdentity(direction, route, name string) string {
	return direction + "\t" + route + "\t" + name
}

// targetConnectionDetail groups the entire retained pool, never the current raw page.
// It uses the same immutable log snapshot as overview, member pages and response samples.
func (r *historyRead) targetConnectionDetail(ctx context.Context, q *pb.TrafficDetailRequest, out *pb.TrafficDetail) error {
	if q.ConnectionSort != "" && q.ConnectionSort != "target" && q.ConnectionSort != "connections" && q.ConnectionSort != "bytes" && q.ConnectionSort != "exit" {
		return errors.New("invalid_filter")
	}
	groups := map[string]*connectionTarget{}
	rules := map[string]*pb.TrafficBasis{}
	exits := map[string]*pb.TrafficExit{}
	var keys []string
	raw := map[string]*pb.FlowRecord{}
	total := uint64(0)
	visit := func(v historystore.ColumnTrafficDetailFact) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		total++
		id, target, kind := connectionTargetIdentity(v.Host, v.IP)
		ruleID := connectionRuleIdentity(v.Source, v.Rule)
		exitID := connectionExitIdentity(v.Direction, v.Route, v.RouteName)
		if rules[ruleID] == nil {
			rules[ruleID] = &pb.TrafficBasis{Rule: v.Rule, Source: v.Source}
		}
		rules[ruleID].Connections++
		if exits[exitID] == nil {
			exits[exitID] = &pb.TrafficExit{Direction: v.Direction, RouteId: v.Route, RouteName: v.RouteName}
		}
		exits[exitID].Connections++
		related := v.ApplicationID
		if r.dimension == "application" {
			related = v.Domain
		}
		if related == "" {
			related = "unknown"
		}
		search := strings.ToLower(v.Host + " " + v.IP + fmt.Sprintf(":%d", v.Port))
		if (q.ConnectionSearch != "" && !strings.Contains(search, strings.ToLower(q.ConnectionSearch))) || (q.ConnectionRule != "" && q.ConnectionRule != ruleID) || (q.ConnectionExit != "" && q.ConnectionExit != exitID) || (q.RelatedId != "" && q.RelatedId != related) {
			return nil
		}
		out.FilteredConnectionCount++
		g := groups[id]
		if g == nil {
			g = &connectionTarget{value: &pb.TrafficConnectionGroup{Id: id, Target: target, Kind: kind}, rules: map[string]*pb.TrafficBasis{}, exits: map[string]*pb.TrafficExit{}}
			groups[id] = g
		}
		g.addEvidence(v)
		g.value.Connections++
		g.value.Upload += v.Upload
		g.value.Download += v.Download
		known := v.BytesState == "complete" || v.BytesState == "partial"
		g.known = g.known || known
		g.incomplete = g.incomplete || v.BytesState != "complete"
		if g.value.LastAt == nil || v.StartedAt.After(g.value.LastAt.AsTime()) {
			g.value.LastAt = timestamppb.New(v.StartedAt)
		}
		if g.rules[ruleID] == nil {
			g.rules[ruleID] = &pb.TrafficBasis{Rule: v.Rule, Source: v.Source}
		}
		g.rules[ruleID].Connections++
		if g.exits[exitID] == nil {
			g.exits[exitID] = &pb.TrafficExit{Direction: v.Direction, RouteId: v.Route, RouteName: v.RouteName}
		}
		x := g.exits[exitID]
		x.Connections++
		x.Upload += v.Upload
		x.Download += v.Download
		if q.TargetGroupId == id {
			keys = append(keys, v.Key)
		}
		return nil
	}
	if r.columnSnapshot != nil && q.ConnectionKey == "" {
		if err := r.columnSnapshot.EachTrafficTargetFact(ctx, r.columnTrafficFilter(q.RowId), visit); err != nil {
			return err
		}
	} else {
		flows := r.flows[q.RowId]
		if r.aggregate {
			flows = nil
			for _, v := range r.aggregateFlowRows[q.RowId] {
				f := flowProto(v.Fact)
				f.Upload, f.Download = v.Upload, v.Download
				flows = append(flows, f)
			}
		}
		if q.ConnectionKey != "" {
			flows = out.Items
		}
		for _, f := range flows {
			v := historystore.ColumnTrafficDetailFact{Key: f.Key, Host: f.Domain, Domain: config.MainDomain(f.Domain), IP: f.Ip, Port: f.Port, ApplicationID: f.ApplicationId, Rule: f.Rule, Source: f.RuleSource, Direction: f.Direction, Route: f.RouteId, RouteName: f.RouteName, StartedAt: f.StartedAt.AsTime(), Upload: f.Upload, Download: f.Download, BytesState: "complete"}
			v.Application, v.Process, v.SourceKind, v.Transport = f.Application, f.Process, f.SourceKind, f.Transport
			v.Failure, v.StateUnknown, v.Closed = f.Failure, f.StateUnknown, f.ClosedAt != nil
			v.LatencyMS, v.LatencyKnown = trafficProtoLatency(f)
			if r.columnSnapshot != nil && q.ConnectionKey != "" {
				saved, ok, err := r.columnSnapshot.Connection(ctx, f.Key)
				if err != nil {
					return err
				}
				if !ok {
					return errors.New("history_expired")
				}
				v.BytesState = saved.Metrics.BytesState
			}
			if err := visit(v); err != nil {
				return err
			}
			raw[f.Key] = f
		}
	}
	out.RetainedConnectionCount = total
	out.TargetGroupTotal = uint64(len(groups))
	out.Items = nil
	out.ConnectionGroups = nil
	out.ConnectionRules = nil
	out.ConnectionExits = nil
	for _, v := range rules {
		out.ConnectionRules = append(out.ConnectionRules, v)
	}
	for _, v := range exits {
		out.ConnectionExits = append(out.ConnectionExits, v)
	}
	sortConnectionRules(out.ConnectionRules)
	sortConnectionExits(out.ConnectionExits)
	var ordered []*pb.TrafficConnectionGroup
	for _, g := range groups {
		g.value.BytesState = "unknown"
		if g.known {
			g.value.BytesState = "complete"
			if g.incomplete {
				g.value.BytesState = "partial"
			}
		}
		for _, v := range g.rules {
			g.value.Rules = append(g.value.Rules, v)
		}
		for _, v := range g.exits {
			g.value.Exits = append(g.value.Exits, v)
		}
		g.finishEvidence()
		sortConnectionRules(g.value.Rules)
		sortConnectionExits(g.value.Exits)
		ordered = append(ordered, g.value)
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		var less, equal bool
		switch q.ConnectionSort {
		case "target":
			less, equal = a.Target < b.Target, a.Target == b.Target
		case "bytes":
			av, bv := a.Upload+a.Download, b.Upload+b.Download
			less, equal = av < bv, av == bv
		case "exit":
			av, bv := connectionGroupExit(a), connectionGroupExit(b)
			less, equal = av < bv, av == bv
		default:
			less, equal = a.Connections < b.Connections, a.Connections == b.Connections
		}
		if equal {
			if !a.LastAt.AsTime().Equal(b.LastAt.AsTime()) {
				return a.LastAt.AsTime().After(b.LastAt.AsTime())
			}
			return a.Id < b.Id
		}
		if q.ConnectionAscending {
			return less
		}
		return !less
	})
	sig := querySignature([]string{q.RowId, q.ConnectionKey, "target-groups-v1", q.TargetGroupId, q.ConnectionSearch, q.ConnectionRule, q.ConnectionExit, q.RelatedId, q.ConnectionSort, fmt.Sprint(q.ConnectionAscending)})
	count := len(ordered)
	if q.TargetGroupId != "" {
		count = len(keys)
		if groups[q.TargetGroupId] == nil {
			return errors.New("history_expired")
		}
	}
	start, end, err := historyBoundPage(q.Cursor, count, trafficDetailLimit(q.Limit), 200, sig)
	if err != nil {
		return err
	}
	out.NextCursor = historyBoundNext(end, count, sig)
	if q.TargetGroupId == "" {
		out.ConnectionGroups = ordered[start:end]
		return nil
	}
	out.ConnectionGroups = []*pb.TrafficConnectionGroup{groups[q.TargetGroupId].value}
	if r.columnSnapshot != nil && q.ConnectionKey == "" {
		items, err := r.columnSnapshot.TrafficConnectionKeys(ctx, r.columnTrafficFilter(q.RowId), keys[start:end])
		if err != nil {
			return err
		}
		for _, v := range items {
			out.Items = append(out.Items, columnTrafficFlow(v))
		}
	} else {
		for _, k := range keys[start:end] {
			out.Items = append(out.Items, raw[k])
		}
	}
	return nil
}
func sortConnectionRules(v []*pb.TrafficBasis) {
	sort.Slice(v, func(i, j int) bool {
		a, b := v[i], v[j]
		if a.Connections != b.Connections {
			return a.Connections > b.Connections
		}
		return connectionRuleIdentity(a.Source, a.Rule) < connectionRuleIdentity(b.Source, b.Rule)
	})
}
func sortConnectionExits(v []*pb.TrafficExit) {
	sort.Slice(v, func(i, j int) bool {
		a, b := v[i], v[j]
		if a.Connections != b.Connections {
			return a.Connections > b.Connections
		}
		return connectionExitIdentity(a.Direction, a.RouteId, a.RouteName) < connectionExitIdentity(b.Direction, b.RouteId, b.RouteName)
	})
}
func connectionGroupExit(v *pb.TrafficConnectionGroup) string {
	var names []string
	for _, x := range v.Exits {
		names = append(names, connectionExitIdentity(x.Direction, x.RouteId, x.RouteName))
	}
	return strings.Join(names, "\x00")
}
