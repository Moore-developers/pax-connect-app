package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"strings"
)

// CompileRouting builds new-connection match rules; no network or upstream types enter this layer.
func CompileRouting(l RoutingLibrary, apps []RoutingApplication, routes map[string]string, ipv6 bool) ([]map[string]any, error) {
	return compileTrafficRouting(l, apps, routes, ipv6, nil)
}

// CompileRoutingEvidence returns labels aligned with the very same compiled rule table.
func CompileRoutingEvidence(l RoutingLibrary, apps []RoutingApplication, routes map[string]string, ipv6 bool) ([]map[string]any, []string, error) {
	labels := []string{}
	rules, err := compileTrafficRouting(l, apps, routes, ipv6, func(e RoutingMatchEvidence) {
		if len([]rune(e.Target)) > 256 {
			e.Target = string([]rune(e.Target)[:256]) + "…"
		}
		raw, _ := json.Marshal(e)
		labels = append(labels, string(raw))
	})
	return rules, labels, err
}

type compiledRoutingRule struct {
	rule     map[string]any
	evidence RoutingMatchEvidence
	user     bool
	fixed    bool
}

func compileTrafficRouting(l RoutingLibrary, apps []RoutingApplication, routes map[string]string, ipv6 bool, record func(RoutingMatchEvidence)) ([]map[string]any, error) {
	compiled, err := compileRouting(l, apps, routes, ipv6)
	if err != nil {
		return nil, err
	}
	if l.WebRTC {
		compiled = applyWebRTCGuard(compiled)
	}
	rules := make([]map[string]any, len(compiled))
	for i, item := range compiled {
		rules[i] = item.rule
		if record != nil {
			record(item.evidence)
		}
	}
	return rules, nil
}

func compileRouting(l RoutingLibrary, apps []RoutingApplication, routes map[string]string, ipv6 bool) ([]compiledRoutingRule, error) {
	if err := l.Validate(); err != nil {
		return nil, err
	}
	policy := RoutingPolicy(ipv6)
	out := []compiledRoutingRule{}
	add := func(rule map[string]any, pack, method, target, destination string) {
		evidence := RoutingMatchEvidence{Package: pack, Method: method, Target: target}
		if destination != "" {
			evidence.UnavailableRoute = routingAction(rule, destination, routes, l.RouteNames)
			if strings.HasPrefix(destination, "route:") {
				evidence.FixedRouteName = l.RouteNames[destination]
				if evidence.FixedRouteName == "" {
					evidence.FixedRouteName = strings.TrimPrefix(destination, "route:")
				}
			}
		}
		out = append(out, compiledRoutingRule{rule: rule, evidence: evidence, user: destination != "", fixed: strings.HasPrefix(destination, "route:")})
	}
	for _, r := range policy {
		if r.Action == "sniff" || r.Action == "hijack-dns" || r.IPVersion == 6 {
			raw, _ := json.Marshal(r)
			var v map[string]any
			_ = json.Unmarshal(raw, &v)
			add(v, "Pax 内置规则", "builtin", r.View.Match, "")
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
		add(m, priority.Name, r.Method, r.Target, r.Destination)
	}
	for _, p := range l.Packages[1:] {
		if !p.Enabled {
			continue
		}
		if p.Kind == "geo" && len(p.GeoContent) > 0 {
			add(map[string]any{"ip_is_private": true, "outbound": "direct"}, "Geo 基础包", "builtin", "局域网与私有网络地址", "")
			google := false
			for _, entry := range p.GeoContent {
				google = google || entry.Target == "geosite-google"
			}
			if !google {
				add(map[string]any{"rule_set": []string{"geosite-google"}, "outbound": "proxy"}, "geosite-google", "builtin", "Google 服务经代理", "")
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
					add(m, tag, "source", description, "")
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
					pack := "Geo 基础包"
					if len(r.RuleSet) == 1 {
						pack = r.RuleSet[0]
					}
					add(v, pack, "builtin", r.View.Match, "")
				}
			}
			continue
		}
		for _, r := range p.RulesForMatching() {
			if !r.Enabled {
				continue
			}
			m, err := routingMatch(r, apps)
			if err != nil {
				return nil, err
			}
			add(m, p.Name, r.Method, r.Target, p.Destination)
		}
	}
	return out, nil
}

func routingAction(m map[string]any, d string, routes, names map[string]string) string {
	switch d {
	case "block":
		m["action"] = "reject"
	case "direct":
		m["outbound"] = "direct"
	case "global":
		m["outbound"] = "proxy"
	default:
		if tag := routes[d]; tag != "" {
			m["outbound"] = tag
			break
		}
		m["action"] = "reject"
		m["no_drop"] = true // Keep returning a rejection even after repeated attempts.
		if name := names[d]; name != "" {
			return name
		}
		return strings.TrimPrefix(d, "route:")
	}
	return ""
}

// applyWebRTCGuard keeps unmatched public STUN on the selector while allowing
// the first matching ordinary action to bind its fixed route, including failure.
// Compile one guard for each preceding run of ordinary actions, preserving
// their STUN precedence without adding copies of intervening rejections.
func applyWebRTCGuard(rules []compiledRoutingRule) []compiledRoutingRule {
	through := 1 // Keep the original guard directly after sniff when all routes are usable.
	for i, item := range rules {
		if item.fixed {
			through = i + 1
		}
	}
	out := make([]compiledRoutingRule, 0, len(rules)+1)
	out = append(out, rules[0])
	start := 1
	for i := start; i < through; i++ {
		if !rules[i].fixed {
			continue
		}
		if start < i {
			out = append(out, webRTCGuard(rules[start:i]))
			out = append(out, rules[start:i]...)
		}
		out = append(out, rules[i])
		start = i + 1
	}
	out = append(out, webRTCGuard(nil))
	return append(out, rules[through:]...)
}

func webRTCGuard(preceding []compiledRoutingRule) compiledRoutingRule {
	conditions := []any{map[string]any{"protocol": "stun"}, map[string]any{"ip_is_private": true, "invert": true}}
	if len(preceding) > 0 {
		matches := make([]any, 0, len(preceding))
		for _, item := range preceding {
			match := maps.Clone(item.rule)
			delete(match, "action")
			delete(match, "outbound")
			matches = append(matches, match)
		}
		conditions = append(conditions, map[string]any{"type": "logical", "mode": "or", "rules": matches})
	}
	return compiledRoutingRule{
		rule:     map[string]any{"type": "logical", "mode": "and", "rules": conditions, "outbound": "proxy"},
		evidence: RoutingMatchEvidence{Package: "WebRTC出口限制", Method: "builtin", Target: "公网STUN经代理，不回退直连"},
	}
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
	case "process":
		m["process_path"] = []string{r.Target}
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
