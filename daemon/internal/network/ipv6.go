package network

import (
	"context"
	"errors"
	"net"
)

// IPv6State describes the physical network, never the IPv6 address of our own TUN.
func physicalIPv6State(ctx context.Context, iface string, addresses []net.Addr) string {
	mode := ipv6Configuration(iface)
	global := false
	for _, address := range addresses {
		ip, _, _ := net.ParseCIDR(address.String())
		if ip != nil && ip.To4() == nil && ip.IsGlobalUnicast() && !ip.IsPrivate() {
			global = true
		}
	}
	if mode != "automatic" || !global {
		return classifyIPv6(mode, global, false, nil)
	}
	// Scope to the physical interface so our TUN cannot create this evidence.
	data, err := readRouteCommand(ctx, "-n", "get", "-inet6", "-ifscope", iface, "2606:4700:4700::1111")
	return classifyIPv6(mode, global, ParseRouteInterface(string(data)) == iface, err)
}

func classifyIPv6(mode string, global, routed bool, routeErr error) string {
	if mode == "unknown" {
		return "unknown"
	}
	if mode == "disabled" || mode == "link_local" {
		if global {
			return "unknown"
		}
		return mode
	}
	if !global || errors.Is(routeErr, ErrNoRoute) {
		return "unavailable"
	}
	if routeErr != nil || !routed {
		return "unknown"
	}
	return "available"
}

// IPv4Only is known absence, not an assumption made after an observation failure.
func (p Physical) IPv4Only() bool {
	return p.IPv6State == "disabled" || p.IPv6State == "link_local" || p.IPv6State == "unavailable"
}

// ErrIPv6Unavailable marks a candidate as inapplicable to this physical network.
var ErrIPv6Unavailable = errors.New("ipv6_unavailable")
