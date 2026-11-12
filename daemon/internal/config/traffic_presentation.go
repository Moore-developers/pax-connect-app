package config

import (
	"encoding/json"
	"strings"
)

// RoutingMatchEvidence is frozen at kernel match time, not looked up in current UI state.
type RoutingMatchEvidence struct{ Package, Method, Target string }

func RecordedRoutingEvidence(rule string) (RoutingMatchEvidence, bool) {
	var value RoutingMatchEvidence
	if len(rule) <= 4096 && json.Unmarshal([]byte(rule), &value) == nil && value.Package != "" {
		return value, true
	}
	return value, false
}

// ipv4OnlyRule recognizes only the recorded built-in IPv6 rejection, including
// the old compiler's Geo label. Current settings and similar user conditions do not qualify.
func ipv4OnlyRule(rule, source string) bool {
	if e, ok := RecordedRoutingEvidence(rule); ok {
		return e.Method == "builtin" && e.Target == "IPv6 流量" && (e.Package == "Geo 基础包" || e.Package == "Pax 内置规则")
	}
	return rule == "builtin=IPv6 流量" && (source == "Pax 包：Geo 基础包" || source == "Pax 包：Pax 内置规则")
}

// TrafficRuleDescription translates recorded kernel facts, never current policy guesses.
func TrafficRuleDescription(rule, source string) (string, string) {
	if ipv4OnlyRule(rule, source) {
		return "Pax 内置规则", "IPv6 流量"
	}
	if e, ok := RecordedRoutingEvidence(rule); ok {
		return e.Package, ReadableRule(e.Method + "=" + e.Target)
	}
	if strings.HasPrefix(source, "Pax 包：") {
		return strings.TrimPrefix(source, "Pax 包："), ReadableRule(rule)
	}
	value := strings.NewReplacer("geosite_cn", "geosite-cn", "geoip_cn", "geoip-cn").Replace(strings.TrimSpace(rule))
	switch {
	case rule == "":
		return "未采集规则", "历史没有保存命中依据"
	case rule == "final":
		return "无", "未命中任何规则，走当前线路"
	case value == "rule_set=geosite-google" || value == "rule_set=[geosite-google]":
		return "geosite-google", "Google 服务经代理"
	case value == "rule_set=geosite-cn" || value == "rule_set=[geosite-cn]":
		return "geosite-cn", "中国域名直连规则"
	case value == "rule_set=geoip-cn" || value == "rule_set=[geoip-cn]":
		return "geoip-cn", "中国 IP 直连规则"
	case rule == "ip_is_private" || rule == "ip_is_private=true":
		return "Geo 基础包", "局域网与私有地址直连规则"
	case strings.HasPrefix(source, "Pax 包："):
		return strings.TrimPrefix(source, "Pax 包："), ReadableRule(rule)
	default:
		if source == "" || source == "内核规则" {
			source = "历史规则（未记录包名）"
		}
		return source, ReadableRule(rule)
	}
}
func ReadableRule(rule string) string {
	if strings.HasPrefix(rule, "builtin=") {
		return strings.TrimPrefix(rule, "builtin=")
	}
	if strings.HasPrefix(rule, "source=") {
		return strings.TrimPrefix(rule, "source=")
	}
	if strings.HasPrefix(rule, "application=") {
		return "应用身份 " + strings.TrimPrefix(rule, "application=")
	}
	for _, pair := range []struct{ key, title string }{{"domain_suffix=", "域名及子域 "}, {"domain=", "域名 "}, {"ip_cidr=", "IP / 网段 "}, {"process_path_regex=", "应用路径匹配 "}, {"process_path=", "应用路径 "}, {"process_name=", "进程 "}} {
		if strings.Contains(rule, pair.key) {
			return strings.ReplaceAll(rule, pair.key, pair.title)
		}
	}
	if len(rule) > 512 {
		return string([]rune(rule)[:min(512, len([]rune(rule)))]) + "…"
	}
	return rule
}

// TrafficRulePackage is a read projection; original match conditions stay in the detail.
func TrafficRulePackage(rule, source string) string {
	pack, _ := TrafficRuleDescription(rule, source)
	if ipv4OnlyRule(rule, source) {
		return pack
	}
	if e, recorded := RecordedRoutingEvidence(rule); recorded && e.Method == "builtin" && e.Target == "IPv6 流量" {
		return pack
	}
	if source == "Pax 包：Geo 基础包" || (source == "Pax 包：geosite-cn" || source == "Pax 包：geoip-cn" || source == "Pax 包：geosite-google") && (strings.HasPrefix(rule, "source=") || strings.HasPrefix(rule, "builtin=")) || (source == "" || source == "内核规则") && (rule == "rule_set=geosite-google" || rule == "rule_set=[geosite-google]" || rule == "ip_is_private" || rule == "ip_is_private=true" || rule == "rule_set=geosite-cn" || rule == "rule_set=[geosite-cn]" || rule == "rule_set=geoip-cn" || rule == "rule_set=[geoip-cn]") {
		return "Geo 基础包"
	}
	return pack
}

// FlowLatency uses first-return-byte evidence; transport-only ready is never response latency.
func FlowLatency(f FlowFact) (uint64, bool) {
	if f.ResponseKnown {
		return f.ResponseMS, true
	}
	if f.EstablishmentState == "response_ready" && f.Evidence == "first_response" {
		return f.EstablishmentMS, true
	}
	return 0, false
}
func TrafficTrigger(f FlowFact) string {
	e, known := RecordedRoutingEvidence(f.Rule)
	if (strings.Contains(f.Rule, "process_") || strings.HasPrefix(f.Rule, "application=") || known && e.Method == "application") && FlowApplicationName(f) != "" {
		return "应用：" + FlowApplicationName(f)
	}
	if MainDomain(f.Domain) != "" {
		return "域名：" + f.Domain
	}
	if f.IP != "" {
		return "IP：" + f.IP
	}
	return "触发目标未记录"
}
func TrafficBasisLine(rule, source string) string {
	pack, description := TrafficRuleDescription(rule, source)
	label := pack
	if rule == "" {
		return "规则未记录"
	}
	switch {
	case (pack == "geosite-cn" || pack == "geoip-cn") && (!strings.HasPrefix(source, "Pax 包：") || strings.HasPrefix(rule, "builtin=") || strings.HasPrefix(rule, "source=")):
		if pack == "geosite-cn" {
			label = "中国域名"
		} else {
			label = "中国 IP"
		}
	case rule == "ip_is_private" || rule == "ip_is_private=true" || rule == "builtin=局域网与私有网络地址":
		label = "局域网"
	case rule == "final":
		label = "无"
	default:
		if _, known := RecordedRoutingEvidence(rule); known || strings.HasPrefix(source, "Pax 包：") {
			label = pack
		} else if description != "" {
			label = description
		}
	}
	return label
}

func TrafficRuleGroup(rule, source string) string {
	if rule == "" {
		return "规则未记录"
	}
	if rule == "final" {
		return "无"
	}
	return TrafficRulePackage(rule, source)
}
