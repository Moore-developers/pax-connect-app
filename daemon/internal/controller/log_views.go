package controller

import (
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/telemetry"
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
