package controller

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// logViewGroupState is the production fold for one ordered group. The SQL
// reader supplies chronological events and an exact SQL distinct-flow count;
// neither event identities nor all related connections need to stay in Go.
type logViewGroupState struct {
	event             config.HistoryEvent
	first, last       time.Time
	count             uint64
	problem, category string
}

type columnGroupStream interface {
	GroupEventEach(context.Context, string, string, int, func(config.HistoryEvent) error) error
	GroupRelated(context.Context, string, string) (uint64, error)
	Connection(context.Context, string) (config.LogConnectionSummary, bool, error)
}

func projectColumnGroupStream(ctx context.Context, source columnGroupStream, view, key string, until time.Time, collection string) (historystore.ColumnGroup, error) {
	var state logViewGroupState
	if err := source.GroupEventEach(ctx, view, key, 0, func(e config.HistoryEvent) error { state.observe(e, view); return nil }); err != nil {
		return historystore.ColumnGroup{}, err
	}
	if state.count == 0 {
		return historystore.ColumnGroup{View: view, Key: key}, nil
	}
	related, err := source.GroupRelated(ctx, view, key)
	if err != nil {
		return historystore.ColumnGroup{}, err
	}
	row := state.product(key, view, related)
	if view != "activity" {
		if until.IsZero() {
			until = state.last
		}
		return columnGroupFromProduct(view, key, until, row), nil
	}
	var latest config.LogConnectionSummary
	if row.ConnectionKey != "" {
		var er error
		latest, _, er = source.Connection(ctx, row.ConnectionKey)
		if er != nil {
			return historystore.ColumnGroup{}, er
		}
	}
	if until.IsZero() {
		until = state.last
		if latest.Metrics.AsOf.After(until) {
			until = latest.Metrics.AsOf
		}
	}
	var fact *config.FlowFact
	metrics := config.ConnectionMetrics{BytesState: "unknown", Reason: "missing_metrics"}
	if err = source.GroupEventEach(ctx, view, key, 1, func(e config.HistoryEvent) error {
		if e.Flow != nil {
			fact = mergeLogFlow(fact, e.Flow)
			if e.Metrics != nil && !e.Metrics.AsOf.After(until) && !e.Metrics.AsOf.Before(metrics.AsOf) {
				metrics = *e.Metrics
			}
		}
		return nil
	}); err != nil {
		return historystore.ColumnGroup{}, err
	}
	return columnGroupFromRecord(view, key, until, activitySummaryRecord(row, fact, metrics, latest, until, collection)), nil
}

func (g *logViewGroupState) observe(e config.HistoryEvent, view string) {
	if g.count == 0 {
		g.first = e.At
	}
	issue, category := logProblem(e)
	oldLevel := g.event.Level
	flow := e.Flow
	if view == "access" || view == "activity" {
		flow = mergeLogFlow(g.event.Flow, e.Flow)
	}
	g.event = e
	g.event.Flow = flow
	g.last = e.At
	g.count++
	if oldLevel == "error" || oldLevel == "warning" && e.Level != "error" {
		g.event.Level = oldLevel
	}
	if issue != "" && (g.problem == "" || category == "failure" || category == "blocked") {
		g.problem, g.category = issue, category
	}
}

func (g *logViewGroupState) product(key, view string, related uint64) *pb.ProductLog {
	row := eventProto(g.event)
	row.RecordId, row.ProblemId = key, g.problem
	row.Occurrences, row.RelatedConnections = g.count, related
	row.FirstAt, row.LastAt = timestamppb.New(g.first), timestamppb.New(g.last)
	row.Category = g.category
	if g.event.Flow != nil {
		row.Flow = flowProto(*g.event.Flow)
	}
	if view == "access" || view == "activity" && g.event.Flow != nil {
		row.Message = logAccessTitle(g.event.Flow)
		if g.event.Flow != nil && g.event.Flow.Failure == "" && (g.category == "failure" || g.category == "warning") {
			row.Message = "阶段未确认：连接出现异常"
		}
	}
	if view == "activity" && g.event.Flow == nil {
		row.Message = standaloneLogStatus(g.event)
	}
	if view == "problems" {
		row.Message = logProblemTitle(g.category)
		if g.event.Flow != nil {
			if message := config.UnavailableRoutingMessage(*g.event.Flow); message != "" {
				row.Message = message
			}
		}
	}
	return row
}

// activitySummaryRecord is shared by the legacy oracle and bounded SQL fold.
func activitySummaryRecord(row *pb.ProductLog, fact *config.FlowFact, m config.ConnectionMetrics, latest config.LogConnectionSummary, until time.Time, collection string) *pb.LogRecord {
	if row.Flow != nil {
		if latest.Fact.Key != "" && !latest.Metrics.AsOf.After(until) && !latest.Metrics.AsOf.Before(m.AsOf) {
			m = latest.Metrics
			fact = mergeLogFlow(fact, &latest.Fact)
			row.Flow = flowProto(*fact)
			row.Message = logAccessTitle(fact)
		}
		if latest.Metrics.AsOf.After(until) && fact.ClosedAt == nil && m.BytesState == "complete" {
			m.BytesState = "partial"
			m.Reason = "historical_checkpoint"
		}
	}
	if fact != nil && fact.ClosedAt == nil && collection != "collecting" && !m.AsOf.IsZero() {
		copy := *fact
		copy.StateUnknown = true
		copy.ObservedUntil = &m.AsOf
		fact = &copy
		row.Flow = flowProto(copy)
	}
	m = deriveConnectionDuration(m, fact, until)
	record := summaryRecord(row, m, until)
	if record.Flow != nil {
		record.Flow = proto.Clone(record.Flow).(*pb.FlowRecord)
	}
	return record
}
