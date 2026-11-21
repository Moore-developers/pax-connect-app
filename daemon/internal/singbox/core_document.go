package singbox

import (
	"context"
	"errors"
	"io"
	"net"
	"reflect"
	"strconv"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/outbound"
	boxdns "github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/service"
	"pax-connect/daemon/internal/config"
)

// CoreProfile builds the product's TUN profile without constructing or probing
// an instance. Only the root core starts it and owns system proxy mutations.
func CoreProfile(ctx context.Context, p config.RouteConfig, rules Rules, mixed string) (string, error) {
	return CoreCatalogProfile(ctx, nil, p, rules, mixed)
}

// CoreCatalogProfile builds one selector over the frozen catalog. A candidate
// that cannot be prepared is omitted; the current route must be usable.
func CoreCatalogProfile(ctx context.Context, profiles []config.RouteConfig, p config.RouteConfig, rules Rules, mixed string) (string, error) {
	host, port, err := net.SplitHostPort(mixed)
	n, parseErr := strconv.Atoi(port)
	if err != nil || parseErr != nil || host != "127.0.0.1" || n < 1024 || n > 65535 {
		return "", ErrConfig
	}
	ctx = runtimeContext(ctx)
	checkCtx, err := catalogValidationContext(ctx)
	if err != nil {
		return "", err
	}
	selectedTag := config.RuntimeTag(p)
	candidates := make([]config.RouteConfig, 0, len(profiles)+1)
	candidates = append(candidates, profiles...)
	candidates = append(candidates, p)
	tagsByID := make(map[string]string, len(candidates))
	p.DNSRouteTags = make(map[string]string, len(candidates))
	var tags []string
	var leaves []option.Outbound
	var servers []option.DNSServerOptions
	for _, candidate := range candidates {
		id := "route:" + candidate.ID
		if tagsByID[id] != "" {
			continue
		}
		if candidate.ID == p.ID {
			candidate = p
		}
		candidate = config.WithIPv4Only(candidate, p.IPv4Only)
		tag := config.RuntimeTag(candidate)
		dnsTag := "dns-route-" + tag
		outbounds, dns, err := catalogRouteOptions(checkCtx, candidate, tag, dnsTag)
		if err != nil {
			if tag == selectedTag {
				return "", err
			}
			// A failed leaf (or either hop of a chain) contributes no tag to
			// the traffic or DNS rule tables.
			continue
		}
		tagsByID[id], p.DNSRouteTags[id] = tag, dnsTag
		tags = append(tags, tag)
		leaves = append(leaves, outbounds...)
		servers = append(servers, dns...)
	}
	if len(tags) > 1000 {
		return "", errors.New("catalog_limit")
	}
	base := p
	base.Front = nil // Selector leaves own all chain-specific DNS and detours.
	opts, err := buildOptions(ctx, base, rules, true, !p.IPv4Only)
	if err != nil {
		return "", err
	}
	opts.Outbounds = append([]option.Outbound{opts.Outbounds[0], {Type: "selector", Tag: "proxy", Options: &option.SelectorOutboundOptions{Outbounds: badoption.Listable[string](tags), Default: selectedTag}}}, leaves...)
	opts.DNS.Servers = append(opts.DNS.Servers, servers...)
	library := config.NewRoutingLibrary()
	if p.Routing != nil {
		library = *p.Routing
	}
	library.WebRTC = p.NetworkPrivacy.WebRTC
	compiled, err := config.CompileRouting(library, p.RoutingApplications, tagsByID, !p.IPv4Only)
	if err != nil {
		return "", err
	}
	rawRules, err := json.Marshal(compiled)
	if err != nil {
		return "", err
	}
	// The custom decoder merges fields. Discard bootstrap rules before decoding.
	opts.Route.Rules = nil
	if err = json.UnmarshalContext(ctx, rawRules, &opts.Route.Rules); err != nil {
		return "", ErrConfig
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

// Native constructors validate options such as packet_encoding that survive
// typed decoding. The validation managers provide only generated dependencies.
// They are never started and perform no network I/O.
func catalogValidationContext(ctx context.Context) (context.Context, error) {
	ctx = service.ContextWithRegistry(ctx, service.RegistryFromContext(ctx).Clone())
	leaves := outbound.NewManager(service.FromContext[adapter.OutboundRegistry](ctx),
		endpoint.NewManager(service.FromContext[adapter.EndpointRegistry](ctx)), "direct")
	resolvers := boxdns.NewTransportManager(service.FromContext[adapter.DNSTransportRegistry](ctx), leaves, "dns-local")
	service.MustRegister[adapter.OutboundManager](ctx, leaves)
	service.MustRegister[adapter.DNSTransportManager](ctx, resolvers)
	// Native constructors only resolve the generated dependency by tag.
	bootstrap := &option.RemoteDNSServerOptions{DNSServerAddressOptions: option.DNSServerAddressOptions{Server: "223.5.5.5"}}
	if err := resolvers.Create(ctx, log.NewNOPFactory().Logger(), "dns-local", "udp", bootstrap); err != nil {
		return nil, ErrConfig
	}
	return ctx, nil
}

func catalogRouteOptions(ctx context.Context, p config.RouteConfig, tag, dnsTag string) ([]option.Outbound, []option.DNSServerOptions, error) {
	if err := config.ValidateAddressFamily(p); err != nil {
		return nil, nil, err
	}
	if p.TLSResolved.NeedsConfirmation || (p.Front != nil && p.Front.TLSResolved.NeedsConfirmation) {
		return nil, nil, config.ErrTLSConfirmation
	}
	documents, dnsServers, err := plannedOutbounds(p, tag)
	if err != nil {
		return nil, nil, err
	}
	logger := log.NewNOPFactory().Logger()
	check := service.FromContext[adapter.OutboundRegistry](ctx)
	dnsServers = append(dnsServers, encryptedDNS(dnsTag, remoteDNSServer, "cloudflare-dns.com", tag))
	servers := make([]option.DNSServerOptions, 0, len(dnsServers))
	for _, server := range dnsServers {
		raw, _ := json.Marshal(server)
		var decoded option.DNSServerOptions
		if err = decoded.UnmarshalJSONContext(ctx, raw); err != nil {
			return nil, nil, ErrConfig
		}
		servers = append(servers, decoded)
	}
	if p.Front != nil {
		// A chain's final hop resolves through its own front, by tag.
		resolvers := service.FromContext[adapter.DNSTransportManager](ctx)
		if err := resolvers.Create(ctx, logger, servers[0].Tag, servers[0].Type, servers[0].Options); err != nil {
			return nil, nil, ErrConfig
		}
	}
	outbounds := make([]option.Outbound, 0, len(documents))
	for _, doc := range documents {
		// Runtime detours and resolvers come from the builder, not a saved
		// document. Keep the same product protocol boundary as the catalog.
		switch doc["type"] {
		case "vmess", "vless", "trojan", "shadowsocks", "hysteria2", "anytls", "socks", "http":
		default:
			return nil, nil, ErrConfig
		}
		raw, err := json.Marshal(doc)
		if err != nil {
			return nil, nil, ErrConfig
		}
		var leaf option.Outbound
		if err = leaf.UnmarshalJSONContext(ctx, raw); err != nil {
			return nil, nil, ErrConfig
		}
		if !coreExactKeys(doc, reflect.ValueOf(leaf)) || !coreAllowedValue(reflect.ValueOf(leaf)) {
			return nil, nil, config.ErrDocumentField
		}
		profileCtx := adapter.WithContext(ctx, &adapter.InboundContext{Outbound: leaf.Tag})
		candidate, err := check.CreateOutbound(profileCtx, nil, logger, leaf.Tag, leaf.Type, leaf.Options)
		if err != nil {
			return nil, nil, ErrConfig
		}
		if closer, ok := candidate.(io.Closer); ok {
			if err := closer.Close(); err != nil {
				return nil, nil, ErrConfig
			}
		}
		outbounds = append(outbounds, leaf)
	}
	return outbounds, servers, nil
}
