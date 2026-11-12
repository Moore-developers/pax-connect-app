package singbox

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
	"net"
	"net/http"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
	"time"
)

var ErrProbe = errors.New("proxy probe failed")

var latencyTargets = []string{"https://www.google.com/generate_204", "https://www.gstatic.com/generate_204"}

// TestRoute runs one bounded real connection through a temporary no-TUN instance and measures
// dial-to-first-response. The caller bounds the context; classification matches Probe.
func (r *Runtime) TestRoute(ctx context.Context, p config.DebugProfile) (time.Duration, error) {
	value, err := r.measureRoute(ctx, p, nil)
	return value.Duration, err
}

// MeasureRoute measures latency only. Exit lookup is a separate user task.
func (r *Runtime) MeasureRoute(ctx context.Context, p config.DebugProfile, report func(config.RouteMeasurement)) (config.RouteMeasurement, error) {
	return r.measureRoute(ctx, p, report)
}

func (r *Runtime) measureRoute(ctx context.Context, p config.DebugProfile, report func(config.RouteMeasurement)) (config.RouteMeasurement, error) {
	var value config.RouteMeasurement
	err := r.withMeasurementOutbound(ctx, p, func(ctx context.Context, outbound adapter.Outbound) error {
		probe, cancel := context.WithTimeout(ctx, measurement.LatencyTimeout)
		client, cleanup := latencyClient(outbound)
		defer cleanup()
		sample, err := measurement.SessionLatency(probe, client, latencyTargets)
		duration := sample.Stable
		if duration == 0 {
			duration = sample.Cold
		} // Legacy consumers use successful reachability, not a warm claim.
		target := "google"
		if len(latencyTargets) > 1 && sample.Target == latencyTargets[1] {
			target = "gstatic"
		}
		value = config.RouteMeasurement{Duration: duration, Cold: sample.Cold, Stable: sample.Stable, StableSamples: sample.StableSamples, StableError: sample.StableError, TestTarget: target, Method: measurement.SessionLatencyMethod, FirstError: sample.FirstError}

		if err != nil {
			cancel()
			if probe.Err() == context.DeadlineExceeded && ctx.Err() == nil {
				return &measurement.Failure{Code: "latency_timeout", Stage: "target_https"}
			}
			var failure *measurement.Failure
			if errors.As(err, &failure) && failure.Code == "tls_certificate_invalid" {
				return &CertificateError{Reason: failure.Reason}
			}
			return errors.Join(ErrProbe, err)
		}
		cancel()
		if report != nil {
			report(value)
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	})
	return value, err
}

// CertificateError retains a sanitized classification from the node TLS dial phase.
type CertificateError struct{ Reason string }

func (e *CertificateError) Error() string { return "tls_certificate_invalid:" + e.Reason }
func classifyCertificate(err error) *CertificateError {
	var verification *tls.CertificateVerificationError
	var expired x509.CertificateInvalidError
	var untrusted x509.UnknownAuthorityError
	var hostname x509.HostnameError
	if errors.As(err, &hostname) {
		return &CertificateError{Reason: "hostname_mismatch"}
	}
	if errors.As(err, &untrusted) {
		return &CertificateError{Reason: "untrusted"}
	}
	if errors.As(err, &expired) {
		if expired.Reason == x509.Expired {
			return &CertificateError{Reason: "expired"}
		}
		return &CertificateError{Reason: "noncompliant"}
	}
	if errors.As(err, &verification) {
		return &CertificateError{Reason: "noncompliant"}
	}
	return nil
}
func latencyClient(outbound adapter.Outbound) (*http.Client, func()) {
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, networkName, address string) (net.Conn, error) {
		conn, err := outbound.DialContext(ctx, networkName, M.ParseSocksaddr(address))
		if err != nil {
			return nil, nodeMeasurementFailure(err)
		}
		return conn, nil
	}, TLSHandshakeTimeout: measurement.LatencyTimeout}
	client := &http.Client{Transport: transport, Timeout: measurement.LatencyTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return client, transport.CloseIdleConnections
}
