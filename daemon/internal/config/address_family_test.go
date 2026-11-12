package config

import (
	"errors"
	"testing"
)

func TestIPv4OnlyAddressFamilyValidatesEachHop(t *testing.T) {
	p := RouteConfig{NodeProfile: NodeProfile{Server: "node.example"}}
	front := RouteConfig{NodeProfile: NodeProfile{Server: "2001:db8::1"}}
	p.Front = &front
	if err := ValidateAddressFamily(p); err != nil {
		t.Fatal(err)
	}
	p = WithIPv4Only(p, true)
	if !p.Front.IPv4Only || !errors.Is(ValidateAddressFamily(p), ErrIPv6Unavailable) {
		t.Fatal("IPv6 front accepted")
	}
	if front.IPv4Only {
		t.Fatal("saved source mutated")
	}
}
