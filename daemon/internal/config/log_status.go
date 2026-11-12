package config

// LogStatus describes the strongest saved evidence, never inferred HTTP success.
func LogStatus(f FlowFact) string {
	if IsDNSControl(f) {
		switch f.Failure {
		case "dns_not_found":
			return "域名解析：域名不存在"
		case "connection_timeout", "dns_timeout":
			return "域名解析：查询超时"
		case "dns_server_failure":
			return "域名解析：服务返回失败"
		}
		if f.Failure != "" {
			return "域名解析：解析失败"
		}
		if f.DNS != nil && f.DNS.Result == "NOERROR" {
			return "域名解析：解析成功"
		}
		if f.StateUnknown {
			return "采集中断：后续状态未知"
		}
		if f.ClosedAt != nil {
			return "域名解析：历史结果未采集"
		}
		return "域名解析：等待结果"
	}
	if f.Direction == "blocked" || f.Failure == "rule_rejected" {
		return "规则处理：已阻断"
	}
	if f.Failure != "" || f.EstablishmentState == "failed" {
		stage := "阶段未确认"
		switch f.Phase {
		case "connecting":
			stage = "建立连接"
		case "routing":
			stage = "线路选择"
		}
		problem := "连接失败，原因未确认"
		switch f.Failure {
		case "dns_failed", "dns_resolution_failed":
			stage, problem = "域名解析", "解析失败"
		case "certificate_invalid", "tls_certificate_invalid":
			stage, problem = "安全握手", "证书校验失败"
		case "connection_timeout":
			problem = "连接超时"
		case "connection_refused":
			problem = "目标拒绝连接"
		case "network_unreachable":
			problem = "网络不可达"
		}
		return stage + "：" + problem
	}
	if f.StateUnknown {
		return "采集中断：后续状态未知"
	}
	_, response := FlowLatency(f)
	if f.ClosedAt != nil {
		if response {
			return "连接结束：此前收到回包"
		}
		if f.EstablishmentState == "ready" || f.EstablishmentState == "response_ready" {
			return "连接结束：此前通道已建立"
		}
		if f.Purpose == "" {
			return "连接结束：历史响应未采集"
		}
		return "连接结束：未观测到回包"
	}
	if response {
		return "响应：已收到回包"
	}
	if f.EstablishmentState == "ready" {
		return "建立连接：通道已建立"
	}
	if f.Phase == "routing" {
		return "线路选择：处理中"
	}
	if f.Phase == "connecting" {
		return "建立连接：等待结果"
	}
	return "阶段未确认：等待结果"
}
