package controller

import (
	"context"
	"errors"
	"net/netip"
	"sort"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

func projectLogView(ctx context.Context, events []config.HistoryEvent, q *pb.LogQuery) ([]*pb.ProductLog, error) {
	ordered := append([]config.HistoryEvent(nil), events...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].At.Equal(ordered[j].At) {
			return ordered[i].Sequence < ordered[j].Sequence
		}
		return ordered[i].At.Before(ordered[j].At)
	})
	type entry struct {
		logViewGroupState
		connections map[string]bool
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
		g := groups[key]
		if g == nil {
			g = &entry{connections: map[string]bool{}}
			groups[key] = g
		}
		g.observe(e, q.View)
		if e.Flow != nil {
			g.connections[e.Flow.Key] = true
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
		row := g.product(key, q.View, uint64(len(g.connections)))
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

type activityProjectionOracle struct {
	logs             []config.HistoryEvent
	summaries        map[string]config.LogConnectionSummary
	records          []*pb.LogRecord
	recordByID       map[string]*pb.LogRecord
	recordEvents     map[string][]string
	userApplications map[string]config.RoutingApplication
	until            time.Time
	meta             historyMetadata
	bytes            int
}

func freezeActivityOracle(ctx context.Context, r *activityProjectionOracle) error {
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
	latestByKey := r.summaries
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		m := config.ConnectionMetrics{BytesState: "unknown", Reason: "missing_metrics"}
		if checkpoint, ok := checkpoints[row.ConnectionKey]; ok {
			m = checkpoint
		}
		record := activitySummaryRecord(row, facts[row.ConnectionKey], m, latestByKey[row.ConnectionKey], r.until, r.meta.Collection)
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
func (r *activityProjectionOracle) recordMatches(v *pb.LogRecord, q *pb.LogQuery) bool {
	if q.Quick == "problems" && v.Flow != nil && v.Flow.ClosedAt == nil && r.meta.Collection != "collecting" && v.Metrics.GetAsOf() != nil {
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
func (r *activityProjectionOracle) recordObject(v *pb.LogRecord, dimension string) (string, string) {
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
			if domain := config.MainDomain(f.Domain); domain != "" {
				return domain, domain
			}
		}
	}
	return "unknown", "未记录"
}
func summaryObjects(ctx context.Context, r *activityProjectionOracle, token string, q *pb.LogQuery) (*pb.LogObjectPage, error) {
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
			if record.Flow != nil {
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
func (r *activityProjectionOracle) materializeRecord(record *pb.LogRecord) *pb.LogRecord {
	return (&historyRead{until: r.until, meta: r.meta, userApplications: r.userApplications}).materializeRecord(record)
}
