package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"pax-connect/daemon/internal/cfoptimizer"
	"pax-connect/daemon/internal/measurement"
	"pax-connect/daemon/internal/network"
	"time"
)

func cfPhysicalProbe(p network.Physical, exits *measurement.ExitLookup) cfoptimizer.Probe {
	return cfoptimizer.Probe{
		Locate: func(ctx context.Context, ip string) (cfoptimizer.Location, error) {
			client, closeIdle := network.PhysicalClient(p, "")
			defer closeIdle()
			value, code := exits.Locate(ctx, client, ip, measurement.ExitQuery{Scope: "physical:" + p.Epoch, Purpose: "cf_region"})
			if code != "" {
				return cfoptimizer.Location{}, errors.New(code)
			}
			return cfoptimizer.Location{CountryCode: value.CountryCode, Country: value.Country, Region: value.Region, City: value.City, Source: value.GeoSource, At: value.GeoAt}, nil
		},
		Validate: func(ctx context.Context) error {
			if err := network.ValidatePhysicalBinding(ctx, p); err != nil {
				return cfoptimizer.ErrEnvironment
			}
			return nil
		},
		TCP: func(ctx context.Context, ip string) (time.Duration, error) {
			started := time.Now()
			conn, err := network.DialPhysical(ctx, p, "tcp", net.JoinHostPort(ip, "443"))
			if err != nil {
				if errors.Is(err, network.ErrPhysicalBinding) || errors.Is(err, network.ErrIPv6Unavailable) {
					return 0, cfoptimizer.ErrEnvironment
				}
				return 0, err
			}
			_ = conn.Close()
			return time.Since(started), nil
		},
		Download: func(ctx context.Context, ip string) (float64, error) {
			client, closeIdle := network.PhysicalClient(p, "")
			defer closeIdle()
			client.Timeout = measurement.DownloadTimeout
			transport, ok := client.Transport.(*http.Transport)
			if !ok {
				return 0, errors.New("cf_transport_unavailable")
			}
			transport.DialContext = func(ctx context.Context, kind, _ string) (net.Conn, error) {
				return network.DialPhysical(ctx, p, kind, net.JoinHostPort(ip, "443"))
			}
			return cfDownloadSample(ctx, client)
		},
	}
}

// The HTTP sampler is shared with route measurements; only the injected client
// chooses a physical CF address instead of a sing-box route outbound.
func cfDownloadSample(ctx context.Context, client measurement.Client) (float64, error) {
	bounded, cancel := context.WithTimeout(ctx, measurement.DownloadTimeout)
	defer cancel()
	value, err := measurement.Download(bounded, client, measurement.DownloadBudget)
	if err != nil {
		if errors.Is(err, network.ErrPhysicalBinding) || errors.Is(err, network.ErrIPv6Unavailable) {
			return 0, cfoptimizer.ErrEnvironment
		}
		return 0, err
	}
	return value.BytesPerSecond, nil
}
