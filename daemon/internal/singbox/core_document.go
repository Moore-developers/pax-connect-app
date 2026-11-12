package singbox

import (
	"context"
	"fmt"
	"github.com/sagernet/sing/common/json/badoption"
	"net"
	"strconv"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json"
	"pax-connect/daemon/internal/config"
)

// CoreProfile builds the product's TUN profile without constructing or probing
// an instance. Only the root core starts it and owns system proxy mutations.
func CoreProfile(ctx context.Context, p config.RouteConfig, rules Rules, mixed string) (string, error) {
	return CoreCatalogProfile(ctx, []config.RouteConfig{p}, p, rules, mixed)
}

// CoreCatalogProfile builds one selector over the frozen catalog. Ordinary
// switches retain the instance/TUN; catalog structural changes require reload.
func CoreCatalogProfile(ctx context.Context, profiles []config.RouteConfig, p config.RouteConfig, rules Rules, mixed string) (string, error) {
	host, port, err := net.SplitHostPort(mixed)
	n, parseErr := strconv.Atoi(port)
	if err != nil || parseErr != nil || host != "127.0.0.1" || n < 1024 || n > 65535 {
		return "", ErrConfig
	}
	if err := config.ValidateAddressFamily(p); err != nil {
		return "", err
	}
	ctx = runtimeContext(ctx)
	p.DNSRouteTags = map[string]string{}
	for _, candidate := range append(profiles, p) {
		p.DNSRouteTags["route:"+candidate.ID] = "dns-route-" + config.RuntimeTag(candidate)
	}
	base := p
	base.Front = nil // Selector leaves below own all chain-specific DNS and detours.
	opts, err := buildOptions(ctx, base, rules, true, !p.IPv4Only)
	if err != nil {
		return "", err
	}
	tags := []string{}
	leaves := []option.Outbound{}
	seen := map[string]bool{}
	for _, candidate := range append(profiles, p) {
		candidate = config.WithIPv4Only(candidate, p.IPv4Only)
		tag := config.RuntimeTag(candidate)
		if seen[tag] {
			continue
		}
		seen[tag] = true
		documents, dnsServers, err := plannedOutbounds(candidate, tag)
		if err != nil {
			return "", err
		}
		for _, server := range dnsServers {
			raw, _ := json.Marshal(server)
			var decoded option.DNSServerOptions
			if err := decoded.UnmarshalJSONContext(ctx, raw); err != nil {
				return "", ErrConfig
			}
			opts.DNS.Servers = append(opts.DNS.Servers, decoded)
		}
		server := encryptedDNS(p.DNSRouteTags["route:"+candidate.ID], "1.1.1.1", "cloudflare-dns.com", tag)
		rawDNS, _ := json.Marshal(server)
		var decodedDNS option.DNSServerOptions
		if err := decodedDNS.UnmarshalJSONContext(ctx, rawDNS); err != nil {
			return "", ErrConfig
		}
		opts.DNS.Servers = append(opts.DNS.Servers, decodedDNS)
		for _, document := range documents {
			raw, err := json.Marshal(document)
			if err != nil {
				return "", ErrConfig
			}
			var leaf option.Outbound
			if err = leaf.UnmarshalJSONContext(ctx, raw); err != nil {
				return "", ErrConfig
			}
			leaves = append(leaves, leaf)
		}
		tags = append(tags, tag)
	}
	if len(tags) > 1000 {
		return "", fmt.Errorf("catalog_limit")
	}
	opts.Outbounds = append([]option.Outbound{opts.Outbounds[0], {Type: "selector", Tag: "proxy", Options: &option.SelectorOutboundOptions{Outbounds: badoption.Listable[string](tags), Default: config.RuntimeTag(p)}}}, leaves...)
	if p.Routing == nil {
		library := config.NewRoutingLibrary()
		p.Routing = &library
	}
	library := *p.Routing
	library.WebRTC = p.NetworkPrivacy.WebRTC
	p.Routing = &library
	if p.Routing != nil {
		tagsByID := map[string]string{}
		for _, candidate := range append(profiles, p) {
			tagsByID["route:"+candidate.ID] = config.RuntimeTag(candidate)
		}
		compiled, err := config.CompileRouting(*p.Routing, p.RoutingApplications, tagsByID, !p.IPv4Only)
		if err != nil {
			return "", err
		}
		raw, err := json.Marshal(compiled)
		if err != nil {
			return "", err
		}
		// Decode into a fresh slice: option.Rule's custom decoder merges fields.
		// Reusing the bootstrap slice can retain a prior rule_set/ip_is_private.
		var routeRules []option.Rule
		if err = json.UnmarshalContext(ctx, raw, &routeRules); err != nil {
			return "", ErrConfig
		}
		opts.Route.Rules = routeRules
	}
	for i := range opts.Inbounds {
		if in, ok := opts.Inbounds[i].Options.(*option.HTTPMixedInboundOptions); ok {
			in.ListenPort = uint16(n)
			in.SetSystemProxy = true
		}
	}
	raw, err := json.MarshalContext(ctx, opts)
	if err != nil {
		return "", ErrConfig
	}
	return string(raw), nil
}
