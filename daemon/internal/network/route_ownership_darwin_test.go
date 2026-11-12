package network

import (
	"context"
	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
	"net/netip"
	"syscall"
	"testing"
)

func TestOwnershipUsesWinningRangesNotShadowedForeignDefault(t *testing.T) {
	rows := []routeOwnership{{netip.MustParsePrefix("0.0.0.0/0"), "utun3"}, {netip.MustParsePrefix("0.0.0.0/1"), "utun7"}, {netip.MustParsePrefix("128.0.0.0/1"), "utun7"}}
	called := false
	got, err := assessRoutedTUNs(context.Background(), "utun7", rows, func(context.Context, string) (string, error) { called = true; return "utun3", nil })
	if err != nil || got.Kind != ConflictNone || called {
		t.Fatal("shadowed default incorrectly won", got, err)
	}
	rows = append(rows, routeOwnership{netip.MustParsePrefix("8.8.0.0/16"), "utun3"})
	got, err = assessRoutedTUNs(context.Background(), "utun7", rows, func(context.Context, string) (string, error) { return "utun3", nil })
	if err != nil || got.Kind != ConflictOtherUTUN {
		t.Fatal("more-specific foreign takeover missed", got, err)
	}
}
func TestOwnershipPrivateHostWithoutMaskIsNotDefault(t *testing.T) {
	row := &route.RouteMessage{Flags: unix.RTF_HOST, Addrs: make([]route.Addr, syscall.RTAX_MAX)}
	row.Addrs[syscall.RTAX_DST] = &route.Inet4Addr{IP: [4]byte{10, 1, 0, 1}}
	prefix, ok := ownershipPrefix(row)
	if !ok || prefix.String() != "10.1.0.1/32" {
		t.Fatal("host misread", prefix)
	}
	if _, ok := publicWitness(prefix); ok {
		t.Fatal("private host became public")
	}
	row.Flags = 0
	if _, ok := ownershipPrefix(row); ok {
		t.Fatal("missing mask inferred a default route")
	}
}
