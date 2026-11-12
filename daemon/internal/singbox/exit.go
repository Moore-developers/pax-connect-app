package singbox

import (
	"context"
	"net/http"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
	"pax-connect/daemon/internal/telemetry"
	"strconv"
	"time"

	"github.com/sagernet/sing-box/adapter"
)

func measureExit(ctx context.Context, outbound adapter.Outbound, lookup *measurement.ExitLookup, scope string) (*config.ExitLocation, string, string) {
	transport := measurementHTTPTransport(outbound)
	defer transport.CloseIdleConnections()
	ctx = measurement.WithExitObserver(ctx, func(v measurement.ExitAttempt) {
		if v.Event != "attempt_complete" {
			return
		}
		telemetry.RecordDiagnostic(telemetry.Diagnostic{Stage: "exit_provider", Phase: v.Provider, Code: v.Code, Reason: strconv.Itoa(v.HTTPStatus), DurationMS: v.Duration.Milliseconds(), Success: v.Code == ""})
	})
	started := time.Now()
	result, code, reason := lookup.QueryDetail(ctx, &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, measurement.ExitQuery{Scope: scope, Purpose: "route_exit"})
	telemetry.RecordDiagnostic(telemetry.Diagnostic{Stage: "exit_stage", Route: telemetry.RouteToken(scope), Code: code, DurationMS: time.Since(started).Milliseconds(), Success: code == ""})
	if code != "" {
		return nil, code, reason
	}
	return &config.ExitLocation{IP: result.IP, CountryCode: result.CountryCode, Country: result.Country, Region: result.Region, City: result.City, ISP: result.ISP, ASN: result.ASN, Source: result.Source, GeoSource: result.GeoSource, GeoError: result.GeoError, At: result.At, GeoAt: result.GeoAt}, "", ""
}

// LookupRouteExit queries the proxy exit directly without a latency preflight.
func (r *Runtime) LookupRouteExit(ctx context.Context, p config.RouteConfig) (config.RouteMeasurement, error) {
	var value config.RouteMeasurement
	var reason string
	err := r.withMeasurementOutbound(ctx, p, func(ctx context.Context, outbound adapter.Outbound) error {
		value.ExitLocation, value.ExitError, reason = measureExit(ctx, outbound, r.exits, config.RuntimeTag(p)+"/"+p.MeasurementNetwork)
		if value.ExitLocation != nil {
			value.ExitIP = value.ExitLocation.IP
		}
		if value.ExitError != "" {
			return &measurement.Failure{Code: value.ExitError, Stage: "exit_lookup", Reason: reason}
		}
		return ctx.Err()
	})
	return value, err
}
