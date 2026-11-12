package config

import (
	"errors"
	"net/netip"
)

var ErrIPv6Unavailable = errors.New("ipv6_unavailable")

// WithIPv4Only freezes a per-network runtime policy without changing saved node data.
func WithIPv4Only(p RouteConfig, only bool) RouteConfig {
	p.IPv4Only = only
	if p.Front != nil {
		front := WithIPv4Only(*p.Front, only)
		p.Front = &front
	}
	return p
}

func ValidateAddressFamily(p RouteConfig) error {
	if p.AddressFamilyState == "unknown" {
		return errors.New("network_observation_unavailable")
	}
	if !p.IPv4Only {
		return nil
	}
	if ip, err := netip.ParseAddr(p.Server); err == nil && ip.Is6() {
		return ErrIPv6Unavailable
	}
	if p.Front != nil {
		return ValidateAddressFamily(*p.Front)
	}
	return nil
}
