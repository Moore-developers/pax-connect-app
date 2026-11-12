package config

import "strings"

// SystemLogCategory uses saved event identity. It never guesses from process names.
func SystemLogCategory(kind, source, title string) string {
	if kind == "current_path" {
		return "verification"
	}
	switch kind {
	case "network_changed", "sleep", "wake", "backend", "recovery":
		return "runtime"
	}
	if source == "Pax" {
		switch {
		case strings.HasPrefix(title, "订阅刷新"):
			return "subscriptions"
		case strings.HasPrefix(title, "规则操作"):
			return "routing"
		case strings.HasPrefix(title, "线路测试"), strings.HasPrefix(title, "下载测速"), strings.HasPrefix(title, "整组测试"), strings.HasPrefix(title, "并发评估"), strings.HasPrefix(title, "CF "):
			return "measurement"
		case strings.HasPrefix(title, "日志设置"), strings.HasPrefix(title, "证书设置"):
			return "settings"
		case strings.HasPrefix(title, "线路操作"):
			return "route-management"
		case strings.HasPrefix(title, "选择线路"), strings.HasPrefix(title, "切换线路"), strings.HasPrefix(title, "线路切换"), strings.HasPrefix(title, "切线"):
			return "routes"
		case strings.HasPrefix(title, "开启代理"), strings.HasPrefix(title, "启用代理"), strings.HasPrefix(title, "关闭代理"), strings.HasPrefix(title, "重新连接"), strings.HasPrefix(title, "重连代理"):
			return "power"
		}
	}
	if source == "sing-box" && kind == "diagnostic" {
		return "kernel"
	}
	return "other"
}

// SystemLogCategories fixes the display and tie-breaking order of real categories.
var SystemLogCategories = []struct{ ID, Name string }{
	{"routes", "线路切换"}, {"power", "代理启停"}, {"verification", "通道验证"}, {"runtime", "网络与后台"}, {"kernel", "内核诊断"}, {"other", "其它系统事件"},
	{"subscriptions", "订阅更新"}, {"routing", "分流规则"}, {"measurement", "检测与测速"}, {"settings", "设置与数据"}, {"route-management", "线路管理"},
}
