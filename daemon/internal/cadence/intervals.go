package cadence

import "time"

// The table of business intervals. Callers adopt these names instead of local literals.
const (
	HistoryIngest       = 5 * time.Second  // observation journal poll
	HistoryLogsFallback = 60 * time.Second // log maintenance safety net; ingest wakes it on new rows
	HistoryTraffic      = 60 * time.Second // traffic aggregation
	HistoryPublish      = 1 * time.Second  // coalesced history change publish
	RouteAutoLatency    = 30 * time.Second // automatic latency probe round
	RoutingAutomatic    = 1 * time.Minute  // automatic routing re-evaluation
	PhysicalEgress      = 60 * time.Second // physical egress fallback re-check
	AIUsageVisible      = 3 * time.Second  // AI usage refresh while the window is visible
	AIUsageHidden       = 2 * time.Minute  // AI usage refresh while hidden
	AIUsagePublish      = 1 * time.Second  // coalesced AI usage state push
	ProxyRefresh        = 1 * time.Second  // proxy status refresh, only while a test/download/egress check is in flight
)

// AIUsage returns the AI usage refresh interval for the current window visibility.
func AIUsage(visible bool) time.Duration {
	if visible {
		return AIUsageVisible
	}
	return AIUsageHidden
}
