package singbox

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
)

// DiagnoseNetwork runs bounded read-only requests on a task-owned no-TUN outbound.
func (r *Runtime) DiagnoseNetwork(ctx context.Context, p config.RouteConfig, mode string) (measurement.NetworkDiagnosticReport, config.RouteMeasurement, error) {
	var report measurement.NetworkDiagnosticReport
	var exit config.RouteMeasurement
	err := r.withMeasurementOutbound(ctx, p, func(ctx context.Context, out adapter.Outbound) error {
		transport := measurementHTTPTransport(out)
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		var probes measurement.NetworkDiagnosticReport
		var workers sync.WaitGroup
		if mode != "sources" {
			workers.Add(1)
			go func() { defer workers.Done(); probes = measurement.DiagnoseNetwork(ctx, client, mode) }()
		}
		bounded, cancel := context.WithTimeout(ctx, 9*time.Second)
		measurement.ObserveDiagnostic(ctx, measurement.DiagnosticProgress{ExitState: "running"})
		exit.ExitLocation, exit.ExitError, _ = measureExit(bounded, out, r.exits, config.RuntimeTag(p)+"/"+p.MeasurementNetwork)
		cancel()
		if exit.ExitLocation != nil {
			measurement.ObserveDiagnostic(ctx, measurement.DiagnosticProgress{ExitState: "complete"})
			exit.ExitIP = exit.ExitLocation.IP
			if mode != "services" {
				measurement.ObserveDiagnostic(ctx, measurement.DiagnosticProgress{SourcesState: "running"})
				report = r.exits.DiagnoseIP(ctx, client, exit.ExitIP, config.RuntimeTag(p)+"/"+p.MeasurementNetwork)
				state := "complete"
				if report.ProviderError != "" {
					state = "failed"
				}
				measurement.ObserveDiagnostic(ctx, measurement.DiagnosticProgress{SourcesState: state})
			} else {
				measurement.ObserveDiagnostic(ctx, measurement.DiagnosticProgress{SourcesState: "skipped"})
			}
		} else {
			measurement.ObserveDiagnostic(ctx, measurement.DiagnosticProgress{ExitState: "failed", SourcesState: "skipped"})
			if mode != "services" {
				report.ProviderError = "exit_unavailable"
			}
		}
		workers.Wait()
		report.Probes = probes.Probes
		return nil
	})
	return report, exit, err
}
