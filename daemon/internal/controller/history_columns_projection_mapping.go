package controller

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

func columnFlowFromProto(f *pb.FlowRecord) *historystore.ColumnProjectedFlow {
	if f == nil {
		return nil
	}
	v := &historystore.ColumnProjectedFlow{Fact: config.FlowFact{Key: f.Key, ApplicationID: f.ApplicationId, Application: f.Application, Process: f.Process, Domain: f.Domain, IP: f.Ip, Port: uint16(f.Port), Transport: f.Transport, RouteID: f.RouteId, RouteName: f.RouteName, Direction: f.Direction, Rule: f.Rule, RuleSource: f.RuleSource, StartedAt: f.StartedAt.AsTime(), EstablishmentState: f.EstablishmentState, EstablishmentMS: f.EstablishmentMs, Failure: f.Failure, Evidence: f.Evidence, StateUnknown: f.StateUnknown, ResponseKnown: f.ResponseKnown, ResponseMS: f.ResponseMs, Purpose: f.Purpose, IPSource: f.IpSource, ProcessStatus: f.ProcessStatus, Phase: f.Phase}, PrimaryDomain: f.PrimaryDomain, SourceKind: f.SourceKind, SourceLabel: f.SourceLabel, SourceEvidence: f.SourceEvidence, Upload: f.Upload, Download: f.Download}
	if f.ClosedAt != nil {
		at := f.ClosedAt.AsTime()
		v.Fact.ClosedAt = &at
	}
	if f.ObservedUntil != nil {
		at := f.ObservedUntil.AsTime()
		v.Fact.ObservedUntil = &at
	}
	if f.Dns != nil {
		v.Fact.DNS = &config.DNSFact{QueryType: f.Dns.QueryType, Result: f.Dns.Result, Answers: append([]string(nil), f.Dns.Answers...)}
	}
	return v
}

func columnFlowProto(v *historystore.ColumnProjectedFlow) *pb.FlowRecord {
	if v == nil {
		return nil
	}
	f := v.Fact
	p := &pb.FlowRecord{Key: f.Key, ApplicationId: f.ApplicationID, Application: f.Application, Process: f.Process, Domain: f.Domain, Ip: f.IP, Port: uint32(f.Port), Transport: f.Transport, RouteId: f.RouteID, RouteName: f.RouteName, Direction: f.Direction, Rule: f.Rule, RuleSource: f.RuleSource, StartedAt: timestamppb.New(f.StartedAt), EstablishmentState: f.EstablishmentState, EstablishmentMs: f.EstablishmentMS, Failure: f.Failure, Evidence: f.Evidence, StateUnknown: f.StateUnknown, ResponseKnown: f.ResponseKnown, ResponseMs: f.ResponseMS, Purpose: f.Purpose, IpSource: f.IPSource, ProcessStatus: f.ProcessStatus, Phase: f.Phase, PrimaryDomain: v.PrimaryDomain, SourceKind: v.SourceKind, SourceLabel: v.SourceLabel, SourceEvidence: v.SourceEvidence, Upload: v.Upload, Download: v.Download}
	if f.ClosedAt != nil {
		p.ClosedAt = timestamppb.New(*f.ClosedAt)
	}
	if f.ObservedUntil != nil {
		p.ObservedUntil = timestamppb.New(*f.ObservedUntil)
	}
	if f.DNS != nil {
		p.Dns = &pb.DNSRecord{QueryType: f.DNS.QueryType, Result: f.DNS.Result, Answers: append([]string(nil), f.DNS.Answers...)}
	}
	return p
}

func columnGroupFromProduct(view, key string, until time.Time, p *pb.ProductLog) historystore.ColumnGroup {
	g := historystore.ColumnGroup{View: view, Key: key, EventID: p.EventId, FirstAt: p.FirstAt.AsTime(), LastAt: p.LastAt.AsTime(), Until: until, Count: p.Occurrences, Related: p.RelatedConnections, Level: int32(p.Level), Title: p.Message, ProblemID: p.ProblemId, Category: p.Category, CategoryName: p.CategoryName, CategorySymbol: p.CategorySymbol, SystemCategory: p.SystemCategory, ApplicationID: p.ApplicationId, Application: p.Application, Domain: p.Domain, IP: p.Ip, RouteID: p.RouteId, RouteName: p.RouteName, Direction: p.Direction, ConnectionKey: p.ConnectionKey, Flow: columnFlowFromProto(p.Flow)}
	if c := p.Connection; c != nil {
		g.HasConnection = true
		g.ConnectionID, g.Session, g.ConnectionRoute, g.Target, g.Stage, g.Code, g.ConnectionDuration = c.ConnectionId, c.Session, c.RouteId, c.Target, c.Stage, c.Code, c.DurationMs
	}
	columnGroupFilters(&g)
	return g
}

func columnGroupFromRecord(view, key string, until time.Time, p *pb.LogRecord) historystore.ColumnGroup {
	g := historystore.ColumnGroup{View: view, Key: key, EventID: p.EventId, FirstAt: p.FirstAt.AsTime(), LastAt: p.LastAt.AsTime(), Until: until, Count: p.EventCount, Level: int32(p.Level), Title: p.Title, Kind: p.Kind, CategoryName: p.CategoryName, CategorySymbol: p.CategorySymbol, SystemCategory: p.SystemCategory, Flow: columnFlowFromProto(p.Flow)}
	if p.Flow != nil {
		g.Application = p.Flow.Application
		g.RouteName = p.Flow.RouteName
	}
	if m := p.Metrics; m != nil {
		g.Metrics = &config.ConnectionMetrics{DurationMS: m.DurationMs, DurationState: m.DurationState, Upload: m.Upload, Download: m.Download, BytesState: m.BytesState, Reason: m.Reason}
		if m.BytesFrom != nil {
			g.Metrics.BytesFrom = m.BytesFrom.AsTime()
		}
		if m.AsOf != nil {
			g.Metrics.AsOf = m.AsOf.AsTime()
		}
	}
	columnGroupFilters(&g)
	return g
}

func columnGroupFilters(g *historystore.ColumnGroup) {
	if g.Flow == nil {
		return
	}
	f := g.Flow.Fact
	g.RulePackage, _ = config.TrafficRuleDescription(f.Rule, f.RuleSource)
	g.Latency, g.LatencyKnown = config.FlowLatency(f)
}

func columnGroupProduct(g historystore.ColumnGroup, e config.HistoryEvent) *pb.ProductLog {
	p := eventProto(e)
	p.Level = pb.LogLevel(g.Level)
	p.Message = g.Title
	p.RecordId = g.Key
	p.ProblemId = g.ProblemID
	p.Occurrences = g.Count
	p.RelatedConnections = g.Related
	p.FirstAt = timestamppb.New(g.FirstAt)
	p.LastAt = timestamppb.New(g.LastAt)
	p.Category = g.Category
	p.CategoryName = g.CategoryName
	p.CategorySymbol = g.CategorySymbol
	p.SystemCategory = g.SystemCategory
	p.ApplicationId, p.Application, p.Domain, p.Ip, p.RouteId, p.RouteName, p.Direction, p.ConnectionKey = g.ApplicationID, g.Application, g.Domain, g.IP, g.RouteID, g.RouteName, g.Direction, g.ConnectionKey
	p.Flow = columnFlowProto(g.Flow)
	p.Connection = nil
	if g.HasConnection {
		p.Connection = &pb.ConnectionEvent{ConnectionId: g.ConnectionID, Session: g.Session, RouteId: g.ConnectionRoute, Target: g.Target, Stage: g.Stage, Code: g.Code, DurationMs: g.ConnectionDuration}
	}
	return p
}

func columnGroupRecord(g historystore.ColumnGroup, e config.HistoryEvent) *pb.LogRecord {
	p := &pb.LogRecord{Kind: g.Kind, RecordId: g.Key, EventId: g.EventID, Level: pb.LogLevel(g.Level), Title: g.Title, Source: e.Source, Flow: columnFlowProto(g.Flow), FirstAt: timestamppb.New(g.FirstAt), LastAt: timestamppb.New(g.LastAt), EventCount: g.Count, AsOf: timestamppb.New(g.Until), OperationFrom: e.OperationFrom, OperationTo: e.OperationTo, SystemCategory: g.SystemCategory, SystemEvent: systemEventProto(e.System), CategoryName: g.CategoryName, CategorySymbol: g.CategorySymbol}
	if g.Metrics != nil {
		p.Metrics = metricsProto(*g.Metrics)
	}
	return p
}
