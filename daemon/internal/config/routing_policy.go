package config

import "fmt"

// DefaultRoutingOutbound is the final selector of the built-in routing policy.
const DefaultRoutingOutbound = "proxy"

// RoutingPolicyRule is the product-owned kernel input plus its readable projection.
// The same definition drives offline inspection and profile generation.
type RoutingPolicyRule struct {
	View        RoutingRule `json:"-"`
	Action      string      `json:"action,omitempty"`
	Outbound    string      `json:"outbound,omitempty"`
	Port        uint16      `json:"port,omitempty"`
	Protocol    string      `json:"protocol,omitempty"`
	IPVersion   int         `json:"ip_version,omitempty"`
	IPIsPrivate bool        `json:"ip_is_private,omitempty"`
	RuleSet     []string    `json:"rule_set,omitempty"`
}

// RoutingPolicy returns fresh policy values. It does not start or query a core.
func RoutingPolicy(ipv6 bool) []RoutingPolicyRule {
	values := []RoutingPolicyRule{
		{View: RoutingRule{Match: "识别连接协议和目标域名", Action: "识别协议", Source: "内核配置"}, Action: "sniff"},
		{View: RoutingRule{Match: "TCP/UDP 53 DNS", Action: "交由 Pax 处理", Source: "内核配置"}, Port: 53, Action: "hijack-dns"},
	}
	if !ipv6 {
		values = append(values, RoutingPolicyRule{View: RoutingRule{Match: "IPv6 流量", Action: "拦截", Source: "内核配置"}, IPVersion: 6, Action: "reject"})
	}
	values = append(values,
		RoutingPolicyRule{View: RoutingRule{Match: "私有网络地址", Action: "直连", Source: "内核配置"}, IPIsPrivate: true, Outbound: "direct"},
		RoutingPolicyRule{View: RoutingRule{Match: "Google 服务（GeoSite Google）", Action: "当前线路", Source: "Geo基础包"}, RuleSet: []string{"geosite-google"}, Outbound: "proxy"},
		RoutingPolicyRule{View: RoutingRule{Match: "中国域名（GeoSite CN）", Action: "直连", Source: "内置 Geo 规则集"}, RuleSet: []string{"geosite-cn"}, Outbound: "direct"},
		RoutingPolicyRule{View: RoutingRule{Match: "中国 IP（GeoIP CN）", Action: "直连", Source: "内置 Geo 规则集"}, RuleSet: []string{"geoip-cn"}, Outbound: "direct"},
	)
	for i := range values {
		values[i].View.ID = fmt.Sprintf("kernel-rule-%d", i+1)
		values[i].View.Order = uint32(i + 1)
	}
	return values
}

// ConfiguredRoutingRules exposes the local policy even before the first proxy run.
func ConfiguredRoutingRules() []RoutingRule {
	policy := RoutingPolicy(true)
	out := make([]RoutingRule, 0, len(policy)+1)
	for _, rule := range policy {
		out = append(out, rule.View)
	}
	return append(out, RoutingRule{ID: "kernel-default", Order: uint32(len(out) + 1), Match: "未命中上述规则的其它目标", Action: "当前线路", Source: "无"})
}
