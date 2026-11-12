package singbox

import (
	"context"
	"net"
	"net/netip"
	"pax-connect/daemon/internal/config"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
)

// WithRouteNetwork owns an isolated outbound while a bounded HTTPS consumer
// uses its dial and DNS functions. It never starts a TUN or changes selection.
func (r *Runtime) WithRouteNetwork(ctx context.Context, p config.RouteConfig, use func(
	func(context.Context, string, string) (net.Conn, error),
	func(context.Context, string) ([]netip.Addr, error),
) error) error {
	return r.withMeasurementOutbound(ctx, p, func(ctx context.Context, outbound adapter.Outbound) error {
		lookup, closeIdle := routeNetworkResolver(outbound)
		defer closeIdle()
		return use(func(ctx context.Context, network, address string) (net.Conn, error) {
			return outbound.DialContext(ctx, network, M.ParseSocksaddr(address))
		}, lookup)
	})
}
