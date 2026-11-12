package controller

import (
	"context"
	"errors"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"net/netip"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"sort"
	"strings"
	"time"
)

func deriveConnectionDuration(m config.ConnectionMetrics, f *config.FlowFact, until time.Time) config.ConnectionMetrics {
	m.DurationState = "unknown"
	m.DurationMS = 0
	if f == nil {
		m.DurationState = "not_applicable"
		m.BytesState = "not_applicable"
		return m
	}
	end := m.AsOf
	switch {
	case f.ClosedAt != nil && !f.ClosedAt.After(until):
		end = *f.ClosedAt
		m.DurationState = "closed"
	case f.StateUnknown && f.ObservedUntil != nil:
		end = *f.ObservedUntil
		m.DurationState = "observed"
	case !end.IsZero():
		m.DurationState = "active"
	}
	if end.After(until) {
		end = until
	}
	if f.StartedAt.IsZero() || end.Before(f.StartedAt) || end.IsZero() {
		m.DurationState = "unknown"
		return m
	}
	m.DurationMS = uint64(end.Sub(f.StartedAt).Milliseconds())
	return m
}
func metricsProto(m config.ConnectionMetrics) *pb.ConnectionMetrics {
	value := &pb.ConnectionMetrics{DurationMs: m.DurationMS, DurationState: m.DurationState, Upload: m.Upload, Download: m.Download, BytesState: m.BytesState, Reason: m.Reason}
	if !m.BytesFrom.IsZero() {
		value.BytesFrom = timestamppb.New(m.BytesFrom)
	}
	if !m.AsOf.IsZero() {
		value.AsOf = timestamppb.New(m.AsOf)
	}
	return value
}
func summaryRecord(row *pb.ProductLog, m config.ConnectionMetrics, at time.Time) *pb.LogRecord {
	kind := "diagnostic"
	if row.Flow != nil {
		kind = "connection"
	} else if row.Source == "Pax" {
		kind = "operation"
		if row.EventKind == "operation" && row.Level == pb.LogLevel_LOG_LEVEL_ERROR {
			name, _, _ := strings.Cut(row.Message, "：")
			row.Message = strings.TrimSuffix(name, "失败") + "：失败"
		}
		if !strings.Contains(row.Message, "：") {
			if row.EventKind == "current_path" {
				row.Message = "通道验证：" + row.Message
			} else if row.Level == pb.LogLevel_LOG_LEVEL_ERROR {
				row.Message += "：失败"
			} else {
				row.Message = strings.TrimSuffix(strings.TrimSuffix(row.Message, "已完成"), "完成") + "：已完成"
			}
		}
	}
	if kind == "diagnostic" && !strings.Contains(row.Message, "：") {
		row.Message = "诊断：" + row.Message
	}
	categoryName, categorySymbol := row.CategoryName, row.CategorySymbol
	if row.SystemEvent != nil {
		kind = "operation"
		if row.EventKind == "diagnostic" {
			kind = "diagnostic"
		}
		categoryName, categorySymbol = row.SystemEvent.GetCategory().GetName(), row.SystemEvent.GetCategory().GetSymbol()
		if categoryName == "" {
			categoryName = row.Source + "事件 · " + row.SystemEvent.GetCategory().GetKey()
			if row.SystemEvent.GetCategory().GetKey() == "" {
				categoryName = row.Source + "事件 · " + strings.Split(row.SystemEvent.Type, ".")[0]
			}
		}
		status := map[string]string{"completed": "已完成", "failed": "失败", "cancelled": "已取消", "passed": "已通过", "inconclusive": "证据不足", "stale": "已失效", "started": "进行中"}[row.SystemEvent.Result]
		if status == "" {
			status = "结果未确认"
		}
		row.Message = row.SystemEvent.Name + "：" + status

	}
	return &pb.LogRecord{SystemEvent: row.SystemEvent, CategoryName: categoryName, CategorySymbol: categorySymbol, SystemCategory: row.SystemCategory, OperationFrom: row.OperationFrom, OperationTo: row.OperationTo, Kind: kind, RecordId: row.RecordId, EventId: row.EventId, Level: row.Level, Title: row.Message, Source: row.Source, Flow: row.Flow, Metrics: metricsProto(m), FirstAt: row.FirstAt, LastAt: row.LastAt, EventCount: row.Occurrences, AsOf: timestamppb.New(at)}
}

// freezeLogRecords runs once per read session. No global latest values escape the token.
func (c *Controller) freezeLogRecords(ctx context.Context, source logReads, r *historyRead) error {
	if r.logVersion != nil {
		return c.freezeLogModelRecords(ctx, source, r)
	}
	return freezeLogRecordsUncached(ctx, source, r)
}
func freezeLogRecordsUncached(ctx context.Context, source logReads, r *historyRead) error {
	rows, err := projectLogView(ctx, r.logs, &pb.LogQuery{View: "activity"})
	if err != nil {
		return err
	}
	r.records = make([]*pb.LogRecord, 0, len(rows))
	// Activity tokens retain one record and frozen event IDs, rather than
	// duplicating every stage and its serialized diagnostic body.
	r.bytes = 0
	r.recordByID = map[string]*pb.LogRecord{}
	r.recordEvents = map[string][]string{}
	facts := map[string]*config.FlowFact{}
	checkpoints := map[string]config.ConnectionMetrics{}
	for _, event := range r.logs {
		key := logViewKey(event, "activity")
		r.recordEvents[key] = append(r.recordEvents[key], event.ID)
	}
	for i := len(r.logs) - 1; i >= 0; i-- {
		event := r.logs[i]
		if event.Flow != nil {
			facts[event.Flow.Key] = mergeLogFlow(facts[event.Flow.Key], event.Flow)
			if event.Metrics != nil && !event.Metrics.AsOf.After(r.until) && !event.Metrics.AsOf.Before(checkpoints[event.Flow.Key].AsOf) {
				checkpoints[event.Flow.Key] = *event.Metrics
			}
		}
	}
	keys := make([]string, 0, len(facts))
	for key := range facts {
		keys = append(keys, key)
	}
	latestByKey := map[string]config.LogConnectionSummary{}
	if r.logVersion != nil {
		for key := range facts {
			if entry := r.logVersion.connections[key]; entry != nil {
				latestByKey[key] = entry.summary
			}
		}
	} else {
		summaries, err := source.LogConnections(ctx, keys)
		if err != nil {
			return err
		}
		for key := range facts {
			stored, ok := summaries[key]
			if !ok {
				continue
			}
			var latest config.LogConnectionSummary
			if err := decodeJSON(stored.Body, &latest); err != nil {
				return err
			}
			latestByKey[key] = latest
		}
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		m := config.ConnectionMetrics{BytesState: "unknown", Reason: "missing_metrics"}
		fact := facts[row.ConnectionKey]
		if row.Flow != nil {
			if checkpoint, ok := checkpoints[row.ConnectionKey]; ok {
				m = checkpoint
			}
			latest := latestByKey[row.ConnectionKey]
			if latest.Fact.Key != "" && !latest.Metrics.AsOf.After(r.until) && !latest.Metrics.AsOf.Before(m.AsOf) {
				m = latest.Metrics
				fact = mergeLogFlow(fact, &latest.Fact)
				row.Flow = flowProto(*fact)
				row.Message = logAccessTitle(fact)
			}
			if latest.Metrics.AsOf.After(r.until) && fact.ClosedAt == nil && m.BytesState == "complete" {
				m.BytesState = "partial"
				m.Reason = "historical_checkpoint"
			}
		}
		if fact != nil && fact.ClosedAt == nil && r.meta.Collection != "collecting" && !m.AsOf.IsZero() {
			copy := *fact
			copy.StateUnknown = true
			copy.ObservedUntil = &m.AsOf
			fact = &copy
			row.Flow = flowProto(copy)
		}
		m = deriveConnectionDuration(m, fact, r.until)
		record := summaryRecord(row, m, r.until)
		if record.Flow != nil {
			record.Flow = proto.Clone(record.Flow).(*pb.FlowRecord)
		}
		r.bytes += proto.Size(record) + 512
		for _, id := range r.recordEvents[record.RecordId] {
			r.bytes += len(id) + 24
		}
		if r.bytes > 24<<20 {
			return errors.New("history_query_busy")
		}
		r.records = append(r.records, record)
		r.recordByID[record.RecordId] = record
	}
	r.logs = nil
	return nil
}
func (r *historyRead) recordMatches(v *pb.LogRecord, q *pb.LogQuery) bool {
	if r.logVersion != nil && q.Quick == "problems" && v.Flow != nil && v.Flow.ClosedAt == nil && r.meta.Collection != "collecting" && v.Metrics.GetAsOf() != nil {
		v = proto.Clone(v).(*pb.LogRecord)
		v.Flow.StateUnknown = true
	}
	_, userApp := r.userApplications[v.RecordId]
	if q.Scope == "system" || q.Dimension == "system" {
		if v.Flow != nil && (userApp || q.Scope == "") {
			return false
		}
	}
	if q.Scope == "access" {
		if v.Flow == nil {
			return false
		}
		if q.Dimension == "application" && !userApp {
			return false
		}
	}
	if q.ConnectionKey != "" && (v.Flow == nil || v.Flow.Key != q.ConnectionKey) {
		return false
	}
	dnsOnly := len(q.GetCriteria().GetKinds()) == 1 && q.Criteria.Kinds[0] == "dns"
	if v.Flow != nil && v.Flow.Purpose == "dns" && !q.IncludeDns && !dnsOnly && q.ConnectionKey == "" && v.Level == pb.LogLevel_LOG_LEVEL_INFO && q.Scope != "system" {
		return false
	}
	if q.Level != "" {
		level := map[string]pb.LogLevel{"error": pb.LogLevel_LOG_LEVEL_ERROR, "warning": pb.LogLevel_LOG_LEVEL_WARNING, "info": pb.LogLevel_LOG_LEVEL_INFO}[q.Level]
		if v.Level != level {
			return false
		}
	}
	id, _ := r.recordObject(v, q.Dimension)
	return (q.ObjectId == "" || q.ObjectId == "all" || q.ObjectId == id) && criteriaMatches(v, q)
}
func (r *historyRead) recordObject(v *pb.LogRecord, dimension string) (string, string) {
	if dimension == "system" {
		if v.Flow != nil {
			return logSystemSource(v)
		}
		name := v.CategoryName
		if name == "" {
			name = v.Source + "事件"
		}
		return v.SystemCategory, name
	}
	if dimension == "application" {
		if app, ok := r.userApplications[v.RecordId]; ok {
			return app.ID, app.Name
		}
	}
	if f := v.Flow; f != nil {
		switch dimension {
		case "application":
			name := strings.TrimSpace(f.Application)
			if name != "" && name != "未识别应用" && name != "未识别的应用" && name != "unknown" {
				id := f.ApplicationId
				if id == "" {
					id = name
				}
				return id, name
			}
		case "ip":
			if ip, err := netip.ParseAddr(strings.TrimSpace(f.Ip)); err == nil {
				return ip.Unmap().String(), ip.Unmap().String()
			}
		default:
			if domain := logDirectoryDomain(f.Domain); domain != "" {
				return domain, domain
			}
		}
	}
	return "unknown", "未记录"
}

func logDirectoryDomain(raw string) string { return config.MainDomain(raw) }

func querySummaryPage(ctx context.Context, r *historyRead, token string, q *pb.LogQuery) (*pb.LogPage, error) {
	var rows []*pb.LogRecord
	for _, record := range r.records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if r.recordMatches(record, q) {
			rows = append(rows, record)
		}
	}
	signature := logViewSignature(q)
	start, end, err := historyBoundPage(q.Cursor, len(rows), int(q.Limit), 100, signature)
	if err != nil {
		return nil, err
	}
	page := make([]*pb.LogRecord, 0, end-start)
	for _, record := range rows[start:end] {
		page = append(page, r.materializeRecord(record))
	}
	return &pb.LogPage{Records: page, Total: uint64(len(rows)), ReadToken: token, NextCursor: historyBoundNext(end, len(rows), signature), History: historyState(r.meta), HeadSequence: r.meta.LogHeadSequence, AsOf: timestamppb.New(r.until), Options: logOptions(r.records)}, nil
}
func summaryObjects(ctx context.Context, r *historyRead, token string, q *pb.LogQuery) (*pb.LogObjectPage, error) {
	out := &pb.LogObjectPage{ReadToken: token, History: historyState(r.meta)}
	filter := proto.Clone(q).(*pb.LogQuery)
	// Legacy single-level queries keep their directory contract. New advanced
	// level selections live in Criteria and apply to both directory and rows.
	filter.ObjectId = ""
	if q.Scope == "" && q.Dimension != "system" {
		filter.Level = ""
	}
	groups := map[string]*pb.LogObject{}
	for _, record := range r.records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !r.recordMatches(record, filter) {
			continue
		}
		out.TotalEvents++
		id, name := r.recordObject(record, q.Dimension)
		if id == "unknown" {
			out.UnknownEvents++
			continue
		}
		if groups[id] == nil {
			symbol := record.CategorySymbol
			if r.logVersion != nil && record.Flow != nil {
				symbol = "gearshape"
				if _, ok := r.userApplications[record.RecordId]; ok {
					symbol = "app"
				}
			}
			groups[id] = &pb.LogObject{Id: id, Name: name, Symbol: symbol}
		}
		groups[id].Count++
	}
	if q.Scope != "" || q.Dimension == "system" {
		out.SelectedObject = groups[q.ObjectId]
	}
	for _, v := range groups {
		if q.Search == "" || strings.Contains(strings.ToLower(v.Name), strings.ToLower(q.Search)) {
			out.Items = append(out.Items, v)
		}
	}
	sort.Slice(out.Items, func(i, j int) bool {
		a, b := out.Items[i], out.Items[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		an, bn := strings.ToLower(a.Name), strings.ToLower(b.Name)
		if an != bn {
			return an < bn
		}
		return a.Id < b.Id
	})
	signature := querySignature([]any{"typed_count_desc_v3", q.Dimension, q.Search, q.ConnectionKey, q.IncludeDns, q.Quick, q.Criteria, q.Level, q.Scope})
	start, end, err := historyBoundPage(q.Cursor, len(out.Items), int(q.Limit), 100, signature)
	if err != nil {
		return nil, err
	}
	out.NextCursor = historyBoundNext(end, len(out.Items), signature)
	out.Items = out.Items[start:end]
	return out, nil
}
