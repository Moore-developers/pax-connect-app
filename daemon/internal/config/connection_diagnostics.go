package config

import "time"

// CoreObservation is an authenticated, read-only root status observation.
type CoreObservation struct {
	Status, Interface, SelectedTag, RuntimeSession string
	RuntimeVersion                                 uint64
}

// ConnectionProbe separates node setup from a fixed, small HTTPS response.
// Zero NodeDuration is unknown, never inferred from starting a core.
type ConnectionProbe struct {
	NodeDuration, HTTPSDuration time.Duration
	NodeCode, HTTPSCode         string
}
