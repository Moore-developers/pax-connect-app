package config

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// CompileRouting builds new-connection match rules; no network or upstream types enter this layer.
func CompileRouting(l RoutingLibrary, apps []RoutingApplication, routes map[string]string, ipv6 bool) ([]map[string]any, error) {
	return compileRouting(l, apps, routes, ipv6, func(RoutingMatchEvidence, bool) {})
}

// CompileRoutingEvidence returns labels aligned with the very same compiled rule table.
func CompileRoutingEvidence(l RoutingLibrary, apps []RoutingApplication, routes map[string]string, ipv6 bool) ([]map[string]any, []string, error) {
	labels := []string{}
	rules, err := compileRouting(l, apps, routes, ipv6, func(e RoutingMatchEvidence, _ bool) {
		if len([]rune(e.Target)) > 256 {
			e.Target = string([]rune(e.Target)[:256]) + "…"
		}
		raw, _ := json.Marshal(e)
		labels = append(labels, string(raw))
	})
	return rules, labels, err
}
func compileRouting(l RoutingLibrary, apps []RoutingApplication, routes map[string]string, ipv6 bool, record func(RoutingMatchEvidence, bool)) ([]map[string]any, error) {
	if err := l.Validate(); err != nil {
		return nil, err
	}
	policy := RoutingPolicy(ipv6)
	out := []map[string]any{}
	for _, r := range policy {
		if r.Action == "sniff" || r.Action == "hijack-dns" || r.IPVersion == 6 {
			raw, _ := json.Marshal(r)
			var v map[string]any
			_ = json.Unmarshal(raw, &v)
			out = append(out, v)
			record(RoutingMatchEvidence{Package: "Pax 内置规则", Method: "builtin", Target: r.View.Match}, false)
			if r.Action == "sniff" && l.WebRTC {
				out = append(out, map[string]any{"type": "logical", "mode": "and", "rules": []any{map[string]any{"protocol": "stun"}, map[string]any{"ip_is_private": true, "invert": true}}, "outbound": "proxy"})
				record(RoutingMatchEvidence{Package: "WebRTC出口限制", Method: "builtin", Target: "公网STUN经代理，不回退直连"}, false)
			}

		}
	}
	priority := l.Packages[0]
	// A matching "follow default" skips later priority entries, but still enters custom packages.
	skipped := []any{}
	for _, r := range priority.Rules {
		if !r.Enabled {
			continue
		}
		m, err := routingMatch(r, apps)
		if err != nil {
			return nil, err
		}
		if r.Destination == "default" {
			skipped = append(skipped, m)
			continue
		}
		if len(skipped) > 0 {
			exclude := map[string]any{"type": "logical", "mode": "or", "rules": append([]any(nil), skipped...), "invert": true}
			m = map[string]any{"type": "logical", "mode": "and", "rules": []any{m, exclude}}
		}
		if err := routingAction(m, r.Destination, routes); err != nil {
			return nil, err
		}
		out = append(out, m)
		record(RoutingMatchEvidence{Package: priority.Name, Method: r.Method, Target: r.Target}, true)
	}
	for _, p := range l.Packages[1:] {

		if !p.Enabled {
			continue
		}
		if p.Kind == "geo" && len(p.GeoContent) > 0 {
			out = append(out, map[string]any{"ip_is_private": true, "outbound": "direct"})
			record(RoutingMatchEvidence{Package: "Geo 基础包", Method: "builtin", Target: "局域网与私有网络地址"}, false)
			google := false
			for _, entry := range p.GeoContent {
				google = google || entry.Target == "geosite-google"
			}
			if !google {
				out = append(out, map[string]any{"rule_set": []string{"geosite-google"}, "outbound": "proxy"})
				record(RoutingMatchEvidence{Package: "geosite-google", Method: "builtin", Target: "Google 服务经代理"}, false)
			}
			// Downloaded order is not routing precedence: Google must precede CN.
			for _, tag := range []string{"geosite-google", "geosite-cn", "geoip-cn"} {
				for _, entry := range p.GeoContent {
					if entry.Target != tag {
						continue
					}
					m, err := routingMatch(entry, apps)
					if err != nil {
						return nil, err
					}
					outbound, description := "direct", "中国域名 / IP 规则"
					if tag == "geosite-google" {
						outbound, description = "proxy", "Google 服务经代理"
					}
					m["outbound"] = outbound
					out = append(out, m)
					record(RoutingMatchEvidence{Package: tag, Method: "source", Target: description}, false)
				}
			}
			continue
		}
		if p.Kind == "geo" {
			for _, r := range policy {
				if r.IPIsPrivate || len(r.RuleSet) > 0 {
					raw, _ := json.Marshal(r)
					var v map[string]any
					_ = json.Unmarshal(raw, &v)
					out = append(out, v)
					pack := "Geo 基础包"
					if len(r.RuleSet) == 1 {
						pack = r.RuleSet[0]
					}
					record(RoutingMatchEvidence{Package: pack, Method: "builtin", Target: r.View.Match}, false)
				}
			}
			continue
		}
		for _, r := range p.Rules {
			if !r.Enabled {
				continue
			}
			m, err := routingMatch(r, apps)
			if err != nil {
				return nil, err
			}
			if err := routingAction(m, p.Destination, routes); err != nil {
				return nil, err
			}
			out = append(out, m)
			record(RoutingMatchEvidence{Package: p.Name, Method: r.Method, Target: r.Target}, true)
		}
	}
	return out, nil
}
func routingAction(m map[string]any, d string, routes map[string]string) error {
	switch d {
	case "block":
		m["action"] = "reject"
	case "direct":
		m["outbound"] = "direct"
	case "global":
		m["outbound"] = "proxy"
	default:
		tag, ok := routes[d]
		if !ok {
			return fmt.Errorf("routing_route_unavailable")
		}
		m["outbound"] = tag
	}
	return nil
}
func routingMatch(r RoutingEntry, apps []RoutingApplication) (map[string]any, error) {
	m := map[string]any{}
	switch r.Method {
	case "source":
		if json.Unmarshal(r.Match, &m) != nil {
			return nil, ErrRoutingLibrary
		}
	case "domain":
		m["domain"] = []string{r.Target}
	case "domain_suffix":
		m["domain_suffix"] = []string{r.Target}
	case "ip", "cidr":
		m["ip_cidr"] = []string{r.Target}
	case "application":
		found := false
		for _, app := range apps {
			if app.ID != r.Target {
				continue
			}
			found = true
			if app.Bundle {
				m["process_path_regex"] = []string{"^" + regexp.QuoteMeta(app.Path) + "/Contents/.*$"}
			} else {
				m["process_path"] = []string{app.Path}
			}
			break
		}
		if !found {
			return nil, fmt.Errorf("routing_application_unavailable")
		}
	default:
		return nil, ErrRoutingLibrary
	}
	return m, nil
}
