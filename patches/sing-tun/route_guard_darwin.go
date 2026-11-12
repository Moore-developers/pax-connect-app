package tun

import (
	"errors"
	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
	"net"
	"net/netip"
	"syscall"
)

// PaxRouteOwnershipGuard identifies the pinned patch required by Pax's TUN
// adapter. Upstream files remain unmodified in the module cache.
const PaxRouteOwnershipGuard = 1

type paxRoute struct {
	destination    netip.Prefix
	gateway        netip.Addr
	interfaceIndex int
}

var paxInterfaceByName = net.InterfaceByName
var paxReadRoutes = paxRouteMessages
var paxWriteRoute = execRoute

// Only successfully acknowledged additions enter the instance's cleanup set.
func (t *NativeTun) paxRecordRoute(destination netip.Prefix, gateway netip.Addr, index int) {
	t.paxRoutes = append(t.paxRoutes, paxRoute{destination, gateway, index})
	t.routeSet = true
}
func (t *NativeTun) paxUnsetRoutes() error {
	var result error
	for _, owned := range t.paxRoutes {
		result = errors.Join(result, t.paxDeleteOwnedRoute(owned))
	}
	return result
}

// Cleanup checks exact destination, mask, gateway, scope and interface. A
// foreign replacement is left alone; closing our utun removes its own remnants.
func (t *NativeTun) paxDeleteOwnedRoute(owned paxRoute) error {
	iface, err := paxInterfaceByName(t.options.Name)
	if err != nil || iface.Index != owned.interfaceIndex {
		return nil
	}
	messages, err := paxReadRoutes()
	if err != nil {
		return err
	}
	for _, message := range messages {
		row, ok := message.(*route.RouteMessage)
		if !ok || row.Index != iface.Index || !paxRoutePrefix(row, owned.destination) {
			continue
		}
		if (row.Flags&unix.RTF_IFSCOPE != 0) != t.options.InterfaceScope {
			continue
		}
		if paxRouteAddr(row, syscall.RTAX_GATEWAY) != owned.gateway {
			continue
		}
		return paxWriteRoute(unix.RTM_DELETE, t.options.InterfaceScope, iface.Index, owned.destination, owned.gateway)
	}
	return nil
}
func paxRouteMessages() ([]route.Message, error) {
	rib, err := route.FetchRIB(unix.AF_UNSPEC, route.RIBTypeRoute, 0)
	if err != nil {
		return nil, err
	}
	return route.ParseRIB(route.RIBTypeRoute, rib)
}
func paxRouteAddr(row *route.RouteMessage, index int) netip.Addr {
	if index >= len(row.Addrs) {
		return netip.Addr{}
	}
	switch address := row.Addrs[index].(type) {
	case *route.Inet4Addr:
		return netip.AddrFrom4(address.IP)
	case *route.Inet6Addr:
		return netip.AddrFrom16(address.IP)
	}
	return netip.Addr{}
}
func paxRoutePrefix(row *route.RouteMessage, prefix netip.Prefix) bool {
	if paxRouteAddr(row, syscall.RTAX_DST) != prefix.Masked().Addr() {
		return false
	}
	mask := paxRouteAddr(row, syscall.RTAX_NETMASK)
	if !mask.IsValid() {
		return prefix.Bits() == 0
	}
	bits, total := net.IPMask(mask.AsSlice()).Size()
	return bits == prefix.Bits() && total == prefix.Addr().BitLen()
}
