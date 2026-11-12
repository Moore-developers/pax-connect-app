package coreclient

import (
	"context"
	"errors"
	"testing"

	"pax-connect/daemon/internal/config"
)

func TestChangedAddressFamilyRefreshesBeforeSelectingIPv6Literal(t *testing.T) {
	s := &Session{lastIPv4Only: true}
	p := config.RouteConfig{NodeProfile: config.NodeProfile{Server: "2001:db8::1"}}
	_, err := s.SwitchRoute(context.Background(), p)
	if err == nil || err.Error() != "runtime_refresh_required" {
		t.Fatal("old IPv4-only policy blocked a new dual-stack activation", err)
	}
	p.IPv4Only = true
	_, err = s.SwitchRoute(context.Background(), p)
	if !errors.Is(err, config.ErrIPv6Unavailable) {
		t.Fatal("unchanged IPv4-only policy accepted IPv6 literal", err)
	}
}
