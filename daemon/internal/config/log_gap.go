package config

// MarkLogGap marks one still-open summary as unknown from its last known time.
// Closed connections keep their recorded result.
func MarkLogGap(summary LogConnectionSummary) LogConnectionSummary {
	if summary.Fact.ClosedAt != nil {
		return summary
	}
	at := summary.Metrics.AsOf
	summary.Fact.StateUnknown = true
	summary.Fact.ObservedUntil = &at
	summary.Metrics.BytesState = "partial"
	summary.Metrics.Reason = "observation_gap"
	return summary
}
