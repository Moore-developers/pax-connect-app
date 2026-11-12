package singbox

import (
	"context"
	"github.com/sagernet/sing-box/adapter"
	"net/http"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
)

const downloadBudget = measurement.DownloadBudget

// ErrDownloadIncomplete indicates a short or otherwise incomplete sample.
var ErrDownloadIncomplete = measurement.ErrDownloadIncomplete

// DownloadRoute consumes at most 8MiB over an isolated no-TUN outbound. Controller
// supplies the 30s deadline and owns global bandwidth exclusion and cancellation.
func (r *Runtime) DownloadRoute(ctx context.Context, p config.RouteConfig) (config.DownloadMeasurement, error) {
	var value config.DownloadMeasurement
	err := r.withMeasurementOutbound(ctx, p, func(ctx context.Context, outbound adapter.Outbound) error {
		transport := measurementHTTPTransport(outbound)
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		measured, err := downloadSample(ctx, client, downloadBudget)
		value = measured
		return err
	})
	return value, err
}
func downloadSample(ctx context.Context, client *http.Client, bytes uint64) (config.DownloadMeasurement, error) {
	value, err := measurement.Download(ctx, client, bytes)
	return config.DownloadMeasurement{Bytes: value.Bytes, Duration: value.Duration, BytesPerSecond: value.BytesPerSecond, Setup: value.Setup, End: value.End}, err
}
