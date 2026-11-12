package singbox

import "pax-connect/daemon/internal/config"

func encryptedDNS(tag, ip, name, detour string) map[string]any {
	server := map[string]any{"type": "https", "tag": tag, "server": ip, "path": "/dns-query", "tls": map[string]any{"enabled": true, "server_name": name}}
	if detour != "" {
		server["detour"] = detour
	}
	return server
}

// Bootstrap is always explicit encrypted direct DNS. It cannot depend on proxy DNS.
func dnsPolicy(p config.RouteConfig, tun bool) (map[string]any, error) {
	mode := p.NetworkPrivacy.DNSMode
	if mode == "" {
		mode = "balanced"
	}
	if mode != "balanced" && mode != "strict" {
		return nil, config.ErrRouteDocument
	}
	rules := []any{}
	strategy := "prefer_ipv4"
	if p.IPv4Only {
		strategy = "ipv4_only"
		rules = append(rules, map[string]any{"query_type": []string{"AAAA"}, "action": "predefined", "rcode": "NOERROR"})
	}
	if tun {
		library := config.NewRoutingLibrary()
		if p.Routing != nil {
			library = *p.Routing
		}
		compiled, err := config.DNSRoutingRules(library, p.RoutingApplications, p.DNSRouteTags, mode)
		if err != nil {
			return nil, err
		}
		for _, rule := range compiled {
			rules = append(rules, rule)
		}
	}
	servers := []any{encryptedDNS("dns-remote", "1.1.1.1", "cloudflare-dns.com", "proxy"), encryptedDNS("dns-local", "223.5.5.5", "dns.alidns.com", ""), encryptedDNS("dns-domestic", "223.5.5.5", "dns.alidns.com", "")}
	if tun {
		servers = append(servers, map[string]any{"type": "local", "tag": "dns-intranet"})
	}
	return map[string]any{"servers": servers, "rules": rules, "final": "dns-remote", "strategy": strategy, "cache_capacity": 4096, "optimistic": false}, nil
}
