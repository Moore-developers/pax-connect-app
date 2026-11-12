//go:build pax_tun_guard

package singbox

import "github.com/sagernet/sing-tun"

// A missing overlay is a compile failure, rather than an unguarded production TUN.
const routeOwnershipGuard = tun.PaxRouteOwnershipGuard == 1
