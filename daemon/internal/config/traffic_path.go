package config

import "time"

// TrafficPathEvidence is passive ingress/routing evidence for one runtime.
// It does not assert remote delivery or attribute all traffic to one browser.
type TrafficPathEvidence struct {
	TUNConnections, MixedConnections, ProxyRoutes, DirectRoutes uint64
	LastTUNConnection, LastMixedConnection                      time.Time
}
