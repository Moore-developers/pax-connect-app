package controller

import (
	"context"
	"errors"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/telemetry"
	"sort"
	"strings"
	"time"
)

func validLogView(v string) bool {
	return v == "activity" || v == "" || v == "access" || v == "problems" || v == "operations"
}
func logProblem(e config.HistoryEvent) (string, string) {
	category := ""
	switch {
	case e.Kind == "rejected":
		category = "blocked"
	case e.Kind == "current_path" && e.Result == "inconclusive":
		category = "unconfirmed"
	case e.Kind == "failed" || e.Level == "error":
		category = "failure"
	case e.Level == "warning" || e.Level == "warn":
		category = "warning"
	}
	if category == "" {
		return "", ""
	}
	identity := []any{category, e.Kind, e.Result}
	if f := e.Flow; f != nil {
		app := f.ApplicationID
		if app == "" {
			app = f.Process
		}
		target := f.Domain
		if target == "" {
			target = f.IP
		}
		identity = append(identity, app, target, f.Port, f.RouteID, f.Direction, f.Failure)
	} else {
		identity = append(identity, e.Source, e.Title)
	}
	return "problem:" + querySignature(identity), category
}
func accessRecordID(key string) string { return "access:" + querySignature(key) }
func logViewKey(e config.HistoryEvent, view string) string {
	switch view {
	case "activity":
		if e.Flow != nil && e.Flow.Key != "" {
			return accessRecordID(e.Flow.Key)
		}
		return "event:" + e.ID
	case "access":
		if e.Flow != nil && e.Flow.Key != "" {
			return accessRecordID(e.Flow.Key)
		}
	case "problems":
		key, _ := logProblem(e)
		return key
	case "operations":
		if e.Source == "Pax" {
			return e.ID
		}
	default:
		return e.ID
	}
	return ""
}
func logViewObject(e config.HistoryEvent, dimension string) (string, string) {
	if dimension == "application" && e.Source == "Pax" {
		return "pax-connect", "Pax Connect"
	}
	return logObject(e, dimension)
}
func logViewSignature(q *pb.LogQuery) string {
	return querySignature([]any{q.View, q.Dimension, q.ObjectId, q.Level, q.ProblemId, q.ConnectionKey, q.IncludeDns, q.Quick, q.Criteria, q.Scope})
}
func mergeLogFlow(old, next *config.FlowFact) *config.FlowFact {
	return telemetry.MergeFlow(old, next)
}

func logAccessTitle(f *config.FlowFact) string {
	if f == nil {
		return "阶段未确认：未关联连接"
	}
	return config.LogStatus(*f)
}
func logProblemTitle(category string) string {
	switch category {
	case "blocked":
		return "策略阻断"
	case "unconfirmed":
		return "当前通道待确认"
	case "warning":
		return "诊断警告"
	default:
		return "连接或操作失败 · 原因未分类"
	}
}
func projectLogView(ctx context.Context, events []config.HistoryEvent, q *pb.LogQuery) ([]*pb.ProductLog, error) {
	ordered := append([]config.HistoryEvent(nil), events...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].At.Equal(ordered[j].At) {
			return ordered[i].Sequence < ordered[j].Sequence
		}
		return ordered[i].At.Before(ordered[j].At)
	})
	type entry struct {
		event             config.HistoryEvent
		first, last       time.Time
		count             uint64
		connections       map[string]bool
		problem, category string
	}
	groups := map[string]*entry{}
	related := map[string]bool{}
	for _, e := range ordered {
		if key, _ := logProblem(e); key == q.ProblemId && key != "" && e.Flow != nil {
			related[e.Flow.Key] = true
		}
	}
	for _, e := range ordered {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if q.ConnectionKey != "" && (e.Flow == nil || e.Flow.Key != q.ConnectionKey) {
			continue
		}
		if q.ProblemId != "" && q.View == "access" && (e.Flow == nil || !related[e.Flow.Key]) {
			continue
		}
		key := logViewKey(e, q.View)
		if key == "" {
			continue
		}
		if q.View == "problems" && q.ProblemId != "" && key != q.ProblemId {
			continue
		}
		issue, category := logProblem(e)
		g := groups[key]
		if g == nil {
			g = &entry{first: e.At, connections: map[string]bool{}}
			groups[key] = g
		}
		oldLevel := g.event.Level
		flow := e.Flow
		if q.View == "access" || q.View == "activity" {
			flow = mergeLogFlow(g.event.Flow, e.Flow)
		}
		g.event = e
		g.event.Flow = flow
		g.last = e.At
		g.count++
		if oldLevel == "error" || oldLevel == "warning" && e.Level != "error" {
			g.event.Level = oldLevel
		}
		if e.Flow != nil {
			g.connections[e.Flow.Key] = true
		}
		if issue != "" && (g.problem == "" || category == "failure" || category == "blocked") {
			g.problem = issue
			g.category = category
		}
	}
	rows := make([]*pb.ProductLog, 0, len(groups))
	for key, g := range groups {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if q.Level != "" && g.event.Level != q.Level {
			continue
		}
		object, _ := logViewObject(g.event, q.Dimension)
		if q.ObjectId != "" && q.ObjectId != "all" && object != q.ObjectId {
			continue
		}
		row := eventProto(g.event)
		row.RecordId = key
		row.ProblemId = g.problem
		row.Occurrences = g.count
		row.RelatedConnections = uint64(len(g.connections))
		row.FirstAt = timestamppb.New(g.first)
		row.LastAt = timestamppb.New(g.last)
		row.Category = g.category
		if g.event.Flow != nil {
			row.Flow = flowProto(*g.event.Flow)
		}
		if q.View == "access" || q.View == "activity" && g.event.Flow != nil {
			row.Message = logAccessTitle(g.event.Flow)
			if g.event.Flow != nil && g.event.Flow.Failure == "" && (g.category == "failure" || g.category == "warning") {
				row.Message = "阶段未确认：连接出现异常"
			}
		}
		if q.View == "activity" && g.event.Flow == nil {
			row.Message = standaloneLogStatus(g.event)
		}
		if q.View == "problems" {
			row.Message = logProblemTitle(g.category)
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.At.AsTime().Equal(b.At.AsTime()) {
			return a.RecordId > b.RecordId
		}
		return a.At.AsTime().After(b.At.AsTime())
	})
	return rows, nil
}
func (c *Controller) queryLogView(ctx context.Context, q *pb.LogQuery) (*pb.LogPage, error) {
	r, token, err := c.logRead(ctx, q)
	if err != nil {
		return nil, err
	}
	if q.View == "activity" {
		return querySummaryPage(ctx, r, token, q)
	}
	rows, err := projectLogView(ctx, r.logs, q)
	if err != nil {
		return nil, err
	}
	sig := logViewSignature(q)
	start, end, err := historyBoundPage(q.Cursor, len(rows), int(q.Limit), 200, sig)
	if err != nil {
		return nil, err
	}
	problemFilter := proto.Clone(q).(*pb.LogQuery)
	problemFilter.View = "problems"
	problemFilter.Level = ""
	problemEvents := r.logs
	if r.logVersion != nil && (q.View == "operations" || q.View == "access") {
		problemEvents = r.problemLogs
	}
	problems, err := projectLogView(ctx, problemEvents, problemFilter)
	if err != nil {
		return nil, err
	}
	return &pb.LogPage{ProblemGroups: uint64(len(problems)), Items: rows[start:end], Total: uint64(len(rows)), ReadToken: token, NextCursor: historyBoundNext(end, len(rows), sig), History: historyState(r.meta), HeadSequence: r.meta.LogHeadSequence}, nil
}
func (c *Controller) queryLogViewObjects(ctx context.Context, q *pb.LogQuery) (*pb.LogObjectPage, error) {
	base := proto.Clone(q).(*pb.LogQuery)
	base.Level = ""
	base.ObjectId = ""
	r, token, err := c.logRead(ctx, base)
	if err != nil {
		return nil, err
	}
	if q.View == "activity" {
		return summaryObjects(ctx, r, token, q)
	}
	rows, err := projectLogView(ctx, r.logs, base)
	if err != nil {
		return nil, err
	}
	out := &pb.LogObjectPage{TotalEvents: uint64(len(rows)), ReadToken: token, History: historyState(r.meta)}
	grouped := map[string]*pb.LogObject{}
	for _, row := range rows {
		event := config.HistoryEvent{Source: row.Source}
		if row.Flow != nil {
			event.Flow = &config.FlowFact{ApplicationID: row.ApplicationId, Application: row.Application, Domain: row.Domain, IP: row.Ip}
		}
		id, name := logViewObject(event, q.Dimension)
		if id == "unknown" {
			out.UnknownEvents++
		}
		if grouped[id] == nil {
			grouped[id] = &pb.LogObject{Id: id, Name: name}
		}
		grouped[id].Count++
	}
	for _, row := range grouped {
		if q.Search == "" || strings.Contains(strings.ToLower(row.Name), strings.ToLower(q.Search)) {
			out.Items = append(out.Items, row)
		}
	}
	sort.Slice(out.Items, func(i, j int) bool {
		a, b := out.Items[i], out.Items[j]
		if a.Name == b.Name {
			return a.Id < b.Id
		}
		return a.Name < b.Name
	})
	sig := querySignature([]string{q.View, q.Dimension, q.Search, q.ProblemId, q.ConnectionKey})
	start, end, err := historyBoundPage(q.Cursor, len(out.Items), int(q.Limit), 100, sig)
	if err != nil {
		return nil, err
	}
	out.NextCursor = historyBoundNext(end, len(out.Items), sig)
	out.Items = out.Items[start:end]
	return out, nil
}
func (c *Controller) logViewDetail(ctx context.Context, source logReads, q *pb.LogDetailRequest) (*pb.LogDetail, error) {
	filter := q.Query
	if filter == nil || filter.View == "" || filter.ReadToken == "" {
		return nil, errors.New("invalid_filter")
	}
	r, _, err := c.logRead(ctx, filter)
	if err != nil {
		return nil, err
	}
	var frozen *pb.LogRecord
	var events []config.HistoryEvent
	if filter.View == "activity" {
		frozen = r.recordByID[q.RecordId]
		if frozen != nil {
			frozen = r.materializeRecord(frozen)
		}
		if frozen == nil || !r.recordMatches(frozen, filter) {
			return nil, errors.New("history_expired")
		}
		row := &pb.ProductLog{EventId: frozen.EventId, RecordId: frozen.RecordId, Flow: frozen.Flow}
		if frozen.Flow != nil {
			row.ConnectionKey = frozen.Flow.Key
		}
		out := &pb.LogDetail{Event: row, Flow: row.Flow, Record: frozen, TotalEvents: uint64(len(r.recordEvents[frozen.RecordId]))}
		for _, id := range r.recordEvents[frozen.RecordId] {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if len(events) >= 200 {
				break
			}
			event, ok, err := readFullLogEvent(ctx, source, id)
			if err != nil {
				return nil, err
			}
			if !ok || event.ID == "" {
				return nil, errors.New("history_expired")
			}
			events = append(events, event)
		}
		return finishLogDetail(ctx, source, filter, r, out, events)
	}
	rows, err := projectLogView(ctx, r.logs, filter)
	if err != nil {
		return nil, err
	}
	var row *pb.ProductLog
	for _, v := range rows {
		if v.RecordId == q.RecordId {
			row = v
			break
		}
	}
	if row == nil {
		return nil, errors.New("history_expired")
	}
	return finishLogDetail(ctx, source, filter, r, &pb.LogDetail{Event: row, Flow: row.Flow}, r.logs)
}

// readFullLogEvent reloads one stored event body: frozen pages deliberately drop raw text
// to keep tokens small, exactly as the legacy read did.
func readFullLogEvent(ctx context.Context, source logReads, id string) (config.HistoryEvent, bool, error) {
	row, ok, err := source.ReadLogEvent(ctx, id)
	if err != nil || !ok {
		return config.HistoryEvent{}, false, err
	}
	event, err := logQueryEvent(row)
	return event, err == nil, err
}

func finishLogDetail(ctx context.Context, source logReads, filter *pb.LogQuery, r *historyRead, out *pb.LogDetail, events []config.HistoryEvent) (*pb.LogDetail, error) {
	for _, e := range events {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		match := e.ID == out.GetEvent().GetEventId()
		if filter.View == "access" || filter.View == "activity" && out.Flow != nil {
			match = e.Flow != nil && e.Flow.Key == out.GetFlow().GetKey()
		}
		if filter.View == "problems" {
			key, _ := logProblem(e)
			match = key == out.GetEvent().GetProblemId()
		}
		if !match {
			continue
		}
		if out.Record == nil {
			out.TotalEvents++
		}
		if len(out.Timeline) < 200 {
			out.Timeline = append(out.Timeline, eventProto(e))
		}
		// The grouped detail path keeps the FIRST eight raw lines of the event body; the
		// event-identity path below keeps the newest eight including diagnostics. This
		// asymmetry is the legacy contract and is asserted by the detail tests.
		if len(out.Raw) < 8 {
			saved := e
			if e.Raw == nil && e.ID != "" {
				full, ok, err := readFullLogEvent(ctx, source, e.ID)
				if err != nil {
					return nil, err
				}
				if ok {
					saved = full
				}
			}
			out.Raw = append(out.Raw, saved.Raw...)
			out.Raw = out.Raw[:min(8, len(out.Raw))]
			out.Truncated = out.Truncated || saved.Truncated
			out.RawOmitted = saved.RawOmitted
		}
	}
	if out.Flow != nil {
		diagnostics, err := source.LogDiagnostics(ctx, out.Flow.Key, r.from.UnixMilli(), r.until.UnixMilli(), r.diagnosticsHead)
		if err != nil {
			return nil, err
		}
		for _, diagnostic := range diagnostics {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if len(out.Raw) < 8 {
				out.Raw = append(out.Raw, diagnostic.Text)
			} else {
				out.Truncated = true
			}
		}
	}
	return out, nil
}

func standaloneLogStatus(e config.HistoryEvent) string {
	if e.Source == "Pax" {
		if e.Kind == "operation" {
			return e.Title
		}
		if e.Kind == "current_path" {
			return e.Title
		}
		if e.Level == "error" {
			return "操作失败"
		}
		return e.Title
	}
	switch e.Result {
	case "dns_failed", "dns_resolution_failed":
		return "域名解析失败"
	case "certificate_invalid", "tls_certificate_invalid":
		return "证书校验失败"
	case "connection_timeout":
		return "连接超时"
	case "connection_refused":
		return "目标拒绝连接"
	case "network_unreachable":
		return "网络不可达"
	}
	if e.Level == "error" {
		return "内核报告错误 · 原因未确认"
	}
	if e.Level == "warning" || e.Level == "warn" {
		return "内核诊断警告"
	}
	return e.Title
}
