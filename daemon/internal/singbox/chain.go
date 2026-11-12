package singbox

import (
	"pax-connect/daemon/internal/config"
)

// plannedOutbounds delegates two-hop transport to native sing-box detour. Final
// endpoint DNS uses encrypted DNS through the front, never a direct fallback.
func plannedOutbounds(p config.RouteConfig, tag string) ([]map[string]any, []map[string]any, error) {
	last, err := routeOutbound(p, tag)
	if err != nil {
		return nil, nil, err
	}
	setResolverFamily(last, p.IPv4Only)
	if p.Front == nil {
		return []map[string]any{last}, nil, nil
	}
	if p.Front.Front != nil {
		return nil, nil, ErrConfig
	}
	frontTag := tag + "-front"
	front, err := routeOutbound(*p.Front, frontTag)
	if err != nil {
		return nil, nil, err
	}
	setResolverFamily(front, p.IPv4Only)
	last["detour"] = frontTag
	delete(last, "bind_interface")
	resolver := tag + "-endpoint-dns"
	last["domain_resolver"] = map[string]any{"server": resolver}
	setResolverFamily(last, p.IPv4Only)
	dns := encryptedDNS(resolver, remoteDNSServer, "cloudflare-dns.com", frontTag)
	return []map[string]any{front, last}, []map[string]any{dns}, nil
}

func setResolverFamily(out map[string]any, ipv4Only bool) {
	if !ipv4Only {
		return
	}
	if resolver, ok := out["domain_resolver"].(map[string]any); ok {
		resolver["strategy"] = "ipv4_only"
	}
}
