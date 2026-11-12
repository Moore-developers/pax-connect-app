package singbox

import (
	"context"
	"net"
	"net/http"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
	"time"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
)

// DiagnoseRoute makes one small HTTPS request over a frozen, isolated outbound.
// It never queries an exit provider, downloads speed data, or changes selection.
func (r *Runtime) DiagnoseRoute(ctx context.Context, p config.RouteConfig) config.ConnectionProbe {
	result := config.ConnectionProbe{}
	nodeResult := make(chan config.ConnectionProbe, 1)
	err := r.withMeasurementOutbound(ctx, p, func(ctx context.Context, out adapter.Outbound) error {
		transport := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 3 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			started := time.Now()
			conn, err := out.DialContext(ctx, network, M.ParseSocksaddr(address))
			sample := config.ConnectionProbe{NodeDuration: time.Since(started)}
			if err != nil {
				sample.NodeCode = nodeMeasurementFailure(err).Code
			}
			select {
			case nodeResult <- sample:
			default:
			}
			if err != nil {
				return nil, nodeMeasurementFailure(err)
			}
			return conn, nil
		}}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		started := time.Now()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, latencyTargets[0], nil)
		if err != nil {
			return err
		}
		response, err := client.Do(req)
		result.HTTPSDuration = time.Since(started)
		if response != nil {
			_ = response.Body.Close()
		}
		if err != nil {
			return err
		}
		if response.StatusCode != http.StatusNoContent {
			return &measurement.Failure{Code: "target_http_failed", Stage: "target_https"}
		}
		return nil
	})
	select {
	case sample := <-nodeResult:
		result.NodeDuration, result.NodeCode = sample.NodeDuration, sample.NodeCode
	default:
	}
	if err != nil && result.NodeCode == "" {
		result.HTTPSCode = measurement.Classify(err, false).Code
	}
	return result
}
