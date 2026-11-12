package config

import "time"

// ConnectionMetrics is evidence carried with a frozen log summary, not HTTP timing.
type ConnectionMetrics struct {
	DurationMS       uint64
	DurationState    string
	Upload, Download uint64
	BytesState       string
	BytesFrom, AsOf  time.Time
	Reason           string
}

// LogConnectionSummary is maintained only by the user history owner.
type LogConnectionSummary struct {
	Fact         FlowFact
	Metrics      ConnectionMetrics
	LastEventID  string
	LastLogAt    time.Time
	LastSequence uint64
}
