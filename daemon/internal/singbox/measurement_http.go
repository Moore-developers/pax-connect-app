package singbox

import (
	"context"
	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
	"net"
	"net/http"
)

// A transport belongs to one frozen measurement task. HTTP keep-alive must not
// force Connection: close on tunneled streams; callers close idle connections
// before returning, so transports/connections never cross task or route boundaries.
func measurementHTTPTransport(outbound adapter.Outbound) *http.Transport {
	return &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, kind, address string) (net.Conn, error) {
		connection, err := outbound.DialContext(ctx, kind, M.ParseSocksaddr(address))
		if err != nil {
			return nil, nodeMeasurementFailure(err)
		}
		return connection, nil
	}}
}
