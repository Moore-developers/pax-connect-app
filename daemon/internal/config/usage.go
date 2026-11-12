package config

import "time"

// UsageCounter is a cumulative watermark for one opaque runtime outbound.
type UsageCounter struct {
	Tag                           string
	Upload, Download, Connections uint64
}
type UsageSnapshot struct {
	Session          string
	Sequence         uint64
	At               time.Time
	Counters         []UsageCounter
	ConnectionsKnown bool
	Partial          bool
}
