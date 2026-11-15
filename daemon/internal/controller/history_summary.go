package controller

import (
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
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
