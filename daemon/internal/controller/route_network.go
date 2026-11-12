package controller

import (
	"context"
	"encoding/json"
	"errors"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/network"
)

// bindRouteInterface freezes the physical path for isolated measurements. The
// final chain hop is reached through native detour; only the front binds locally.
func bindRouteInterface(p config.RouteConfig, name string) config.RouteConfig {
	if name == "" {
		return p
	}
	if p.Front != nil {
		front := bindRouteInterface(*p.Front, name)
		p.Front = &front
		return p
	}
	var doc map[string]any
	if json.Unmarshal(config.DocumentForRoute(p), &doc) != nil {
		return p
	}
	doc["bind_interface"] = name
	p.Document, _ = json.Marshal(doc)
	return p
}

func bindRouteNetwork(p config.RouteConfig, physical network.Physical) config.RouteConfig {
	p = config.WithIPv4Only(p, physical.IPv4Only())
	p.AddressFamilyState = physical.IPv6State
	return bindRouteInterface(p, physical.Name)
}

func (c *Controller) freezeRouteFamily(ctx context.Context, p config.RouteConfig) (config.RouteConfig, error) {
	if c.physical != nil {
		physical, err := c.physical.sample(ctx)
		if err != nil || physical.IPv6State == "unknown" {
			return p, errors.New("network_observation_unavailable")
		}
		p = config.WithIPv4Only(p, physical.IPv4Only())
	}
	return p, config.ValidateAddressFamily(p)
}
