package network

import (
	"context"
	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
	"net"
	"net/netip"
	"strings"
	"syscall"
)

type routeOwnership struct {
	prefix netip.Prefix
	iface  string
}

func nativeRouteOwnership(ctx context.Context) ([]routeOwnership, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	rib, err := route.FetchRIB(unix.AF_UNSPEC, route.RIBTypeRoute, 0)
	if err != nil {
		return nil, err
	}
	messages, err := route.ParseRIB(route.RIBTypeRoute, rib)
	if err != nil {
		return nil, err
	}
	if len(messages) > 4096 {
		return nil, errObservationUnavailable
	}
	rows := make([]routeOwnership, 0, len(messages))
	names := map[int]string{}
	for _, message := range messages {
		row, ok := message.(*route.RouteMessage)
		if !ok || row.Flags&unix.RTF_UP == 0 || row.Flags&(unix.RTF_REJECT|unix.RTF_BLACKHOLE|unix.RTF_IFSCOPE|unix.RTF_WASCLONED) != 0 || row.Index == 0 {
			continue
		}
		prefix, ok := ownershipPrefix(row)
		if !ok {
			continue
		}
		name := names[row.Index]
		if name == "" {
			iface, e := net.InterfaceByIndex(row.Index)
			if e != nil {
				return nil, e
			}
			name = iface.Name
			names[row.Index] = name
		}
		rows = append(rows, routeOwnership{prefix, name})
	}
	return rows, nil
}
func ownershipPrefix(row *route.RouteMessage) (netip.Prefix, bool) {
	if len(row.Addrs) <= syscall.RTAX_DST {
		return netip.Prefix{}, false
	}
	address := routeAddress(row.Addrs[syscall.RTAX_DST])
	if !address.IsValid() {
		return netip.Prefix{}, false
	}
	if row.Flags&unix.RTF_HOST != 0 {
		return netip.PrefixFrom(address, address.BitLen()), true
	}
	var mask netip.Addr
	if len(row.Addrs) > syscall.RTAX_NETMASK {
		mask = routeAddress(row.Addrs[syscall.RTAX_NETMASK])
	}
	if !mask.IsValid() {
		if address.IsUnspecified() {
			return netip.PrefixFrom(address, 0), true
		}
		return netip.Prefix{}, false
	}
	bits, total := net.IPMask(mask.AsSlice()).Size()
	if total != address.BitLen() {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(address, bits).Masked(), true
}
func routeAddress(address route.Addr) netip.Addr {
	switch a := address.(type) {
	case *route.Inet4Addr:
		return netip.AddrFrom4(a.IP)
	case *route.Inet6Addr:
		return netip.AddrFrom16(a.IP)
	}
	return netip.Addr{}
}

// uncoveredRoute removes more-specific destinations before deciding whether a
// foreign route can win any traffic. Equal-prefix priority is verified by the OS.
func uncoveredRoute(prefix, cover netip.Prefix) []netip.Prefix {
	if prefix.Addr().BitLen() != cover.Addr().BitLen() {
		return []netip.Prefix{prefix}
	}
	if cover.Bits() <= prefix.Bits() && cover.Contains(prefix.Addr()) {
		return nil
	}
	if !prefix.Contains(cover.Addr()) {
		return []netip.Prefix{prefix}
	}
	bits := prefix.Bits() + 1
	left := netip.PrefixFrom(prefix.Addr(), bits)
	raw := prefix.Addr().AsSlice()
	raw[prefix.Bits()/8] |= 1 << uint(7-prefix.Bits()%8)
	rightAddress, _ := netip.AddrFromSlice(raw)
	right := netip.PrefixFrom(rightAddress, bits)
	return append(uncoveredRoute(left, cover), uncoveredRoute(right, cover)...)
}
func publicWitness(prefix netip.Prefix) (netip.Addr, bool) {
	candidates := []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("2606:4700:4700::1111"), prefix.Addr(), prefix.Addr().Next()}
	raw := prefix.Addr().AsSlice()
	for bit := prefix.Bits(); bit < len(raw)*8; bit++ {
		raw[bit/8] |= 1 << uint(7-bit%8)
	}
	last, _ := netip.AddrFromSlice(raw)
	candidates = append(candidates, last)
	for _, a := range candidates {
		if a.IsValid() && prefix.Contains(a) && a.IsGlobalUnicast() && !a.IsPrivate() && !a.IsLoopback() && !a.IsLinkLocalUnicast() {
			return a, true
		}
	}
	return netip.Addr{}, false
}
func assessRoutedTUNs(ctx context.Context, own string, rows []routeOwnership, lookup func(context.Context, string) (string, error)) (ConflictResult, error) {
	lookups := 0
	for _, candidate := range rows {
		if ctx.Err() != nil {
			return ConflictResult{}, ctx.Err()
		}
		if candidate.iface == own || !strings.HasPrefix(candidate.iface, "utun") {
			continue
		}
		visible := []netip.Prefix{candidate.prefix}
		for _, other := range rows {
			if ctx.Err() != nil {
				return ConflictResult{}, ctx.Err()
			}
			if other.prefix.Bits() <= candidate.prefix.Bits() {
				continue
			}
			var next []netip.Prefix
			for _, prefix := range visible {
				next = append(next, uncoveredRoute(prefix, other.prefix)...)
				if len(next) > 128 {
					return ConflictResult{}, errObservationUnavailable
				}
			}
			visible = next
			if len(visible) == 0 {
				break
			}
		}
		for _, prefix := range visible {
			target, ok := publicWitness(prefix)
			if !ok {
				continue
			}
			lookups++
			if lookups > 32 {
				return ConflictResult{}, errObservationUnavailable
			}
			if ctx.Err() != nil {
				return ConflictResult{}, ctx.Err()
			}
			iface, err := lookup(ctx, target.String())
			if err != nil {
				return ConflictResult{}, err
			}
			if strings.HasPrefix(iface, "utun") && iface != own {
				return ConflictResult{Kind: ConflictOtherUTUN, Interface: iface, Target: target.String()}, nil
			}
		}
	}
	return ConflictResult{Kind: ConflictNone}, nil
}

// AssessOwnedConflict verifies the actual winning public ranges. Only the
// runtime's own interface is recognized as Pax, never an address lookalike.
func AssessOwnedConflict(ctx context.Context, own string) ConflictResult {
	rows, err := nativeRouteOwnership(ctx)
	if err != nil {
		return ConflictResult{Kind: ConflictObservationError}
	}
	detailed, err := assessRoutedTUNs(ctx, own, rows, RouteInterface)
	if err != nil {
		return ConflictResult{Kind: ConflictObservationError}
	}
	if detailed.Kind == ConflictOtherUTUN {
		return detailed
	}
	return assessConflict(ctx, RouteInterface, func() (string, error) { return own, nil })
}
