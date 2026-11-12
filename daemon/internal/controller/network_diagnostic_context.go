package controller

import (
	"context"
	"fmt"
	"strings"

	pb "pax-connect/daemon/gen/proto/paxv1"
)

// Diagnostic context is frozen under the existing configuration owner. Domain
// candidates are saved-rule evidence, never a second routing evaluator.
func (c *Controller) captureNetworkContext(ctx context.Context) ([]*pb.NetworkDiagnosticFact, string, error) {
	var facts []*pb.NetworkDiagnosticFact
	var token string
	err := c.query(ctx, func(context.Context) {
		add := func(group, label, value, source, state string) {
			facts = append(facts, &pb.NetworkDiagnosticFact{Group: group, Label: label, Value: value, Source: source, State: state})
		}
		if c.privacy != nil && c.privacy.failure == nil {
			p := c.privacy.value
			token = fmt.Sprint(p.Revision)
			mode := "加密分流"
			if p.DNSMode == "strict" {
				mode = "严格 DNS 隐私"
			}
			add("policy", "DNS 模式", mode, "已保存设置", "information")
			rtc := "关闭"
			if p.WebRTC {
				rtc = "已开启公网 STUN 出口限制"
			}
			add("policy", "公网 STUN", rtc, "已保存设置", "information")
		} else {
			add("policy", "DNS 模式", "未取得设置", "本机", "unknown")
			add("policy", "公网 STUN", "未取得设置", "本机", "unknown")
		}
		if c.routing == nil || c.routing.loadError != nil {
			for _, domain := range []string{"claude.ai", "api.anthropic.com"} {
				add("routing", domain, "未取得已保存规则", "本机规则", "unknown")
			}
			return
		}
		library := c.routing.library
		token += "/" + fmt.Sprint(library.Revision)
		for _, domain := range []string{"claude.ai", "api.anthropic.com"} {
			matches := []string{}
			for _, pack := range library.Packages {
				if !pack.Enabled || pack.Kind == "geo" {
					continue
				}
				for _, entry := range pack.Rules {
					if !entry.Enabled {
						continue
					}
					// Only explicit domain predicates can be associated without application,
					// resolved IP, rule-set contents or the actual runtime connection.
					exact := entry.Method == "domain" && strings.EqualFold(entry.Target, domain)
					suffix := strings.TrimPrefix(strings.ToLower(entry.Target), ".")
					trailing := entry.Method == "domain_suffix" && (domain == suffix || strings.HasSuffix(domain, "."+suffix))
					if !exact && !trailing {
						continue
					}
					destination := pack.Destination
					if pack.Kind == "priority" {
						destination = entry.Destination
					}
					display := map[string]string{"direct": "直连", "block": "阻断", "global": "当前线路", "default": "跟随默认"}[destination]
					if display == "" {
						display = "指定线路"
						if c.routes != nil {
							if record, ok := c.routes.catalog.Find(strings.TrimPrefix(destination, "route:")); ok {
								display = record.Config.Name
							}
						}
					}
					matches = append(matches, pack.Name+" · "+entry.Target+" → "+display)
					if len(matches) >= 8 {
						break
					}
				}
				if len(matches) >= 8 {
					break
				}
			}
			value := "未见显式域名规则；可能命中应用、规则包或 Geo"
			if len(matches) > 0 {
				value = strings.Join(matches, "；")
			}
			add("routing", domain, value, "已保存候选规则", "information")
		}
	})
	return facts, token, err
}
