package controller

import (
	"pax-connect/daemon/internal/config"
	"strings"
)

// operationEvidence adapts existing operation producers to stable event types.
// Labels are chosen by code at admission; historical message parsing stays separate.
func operationEvidence(label, failure, from, to string) *config.SystemEvent {
	var action, name, category, categoryName string
	switch label {
	case "开启代理":
		action, name, category, categoryName = "proxy.enable", label, "power", "代理启停"
	case "关闭代理":
		action, name, category, categoryName = "proxy.disable", label, "power", "代理启停"
	case "切换线路":
		action, name, category, categoryName = "route.switch", label, "routes", "线路切换"
	case "线路操作":
		action, name, category, categoryName = "route.manage", label, "route-management", "线路管理"
	case "订阅刷新":
		action, name, category, categoryName = "subscription.refresh", label, "subscriptions", "订阅更新"
	case "规则操作":
		action, name, category, categoryName = "routing.change", label, "routing", "分流规则"
	case "线路测试", "下载测速", "整组测试", "并发评估":
		action, name, category, categoryName = "measurement."+map[string]string{"线路测试": "route", "下载测速": "download", "整组测试": "batch", "并发评估": "parallel"}[label], label, "measurement", "检测与测速"
	case "日志设置", "证书设置":
		action, name, category, categoryName = "settings."+map[string]string{"日志设置": "history", "证书设置": "certificate"}[label], label, "settings", "设置与数据"
	case "验证当前通道":
		action, name, category, categoryName = "verification.request", label, "verification", "通道验证"
	default:
		if strings.HasPrefix(label, "CF ") {
			action, name, category, categoryName = "measurement.cf", label, "measurement", "检测与测速"
		} else {
			action, name, category, categoryName = "operation.result", label, "operations", "其它系统事件"
		}
	}
	result := "completed"
	if failure != "" {
		result = "failed"
	}
	if failure == "operation_cancelled" {
		result = "cancelled"
	}
	e := &config.SystemEvent{Type: action, Name: name, Category: config.EventCategory{Key: category, Name: categoryName, Symbol: "gearshape"}, Result: result, ErrorCode: failure}
	if from != "" || to != "" {
		e.Sections = append(e.Sections, config.EventSection{Kind: "changes", Title: "线路变化", Fields: []config.EventField{{Label: "线路", Before: from, After: to}}})
	}
	return e
}

func sampleText(known bool, value string) string {
	if !known {
		return "—（无有效样本）"
	}
	return value
}
