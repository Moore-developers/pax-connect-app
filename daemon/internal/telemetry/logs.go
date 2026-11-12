package telemetry

import (
	"math"
	"time"

	"pax-connect/daemon/internal/config"
)

// MergeFlow folds a newer observation into the facts already known for a connection.
// Only missing evidence is inherited; newer explicit values always win.
func MergeFlow(old, next *config.FlowFact) *config.FlowFact {
	if old == nil {
		if next == nil {
			return nil
		}
		copy := *next
		return &copy
	}
	if next == nil {
		return old
	}
	value := *next
	if value.Domain == "" {
		value.Domain = old.Domain
	}
	if value.IP == "" {
		value.IP = old.IP
		value.IPSource = old.IPSource
	}
	if value.ApplicationID == "" {
		value.ApplicationID = old.ApplicationID
		value.Application = old.Application
		value.Process = old.Process
	}
	if value.ProcessStatus == "" {
		value.ProcessStatus = old.ProcessStatus
	}
	if value.Purpose == "" {
		value.Purpose = old.Purpose
	}
	if value.DNS == nil {
		value.DNS = old.DNS
	}
	if value.Direction != "blocked" && (value.RouteID == "" || value.Direction == "unknown") {
		value.RouteID = old.RouteID
		value.RouteName = old.RouteName
		value.Direction = old.Direction
		value.Rule = old.Rule
		value.RuleSource = old.RuleSource
	}
	if value.Phase == "" {
		value.Phase = old.Phase
	}
	if value.Failure == "" {
		value.Failure = old.Failure
	}
	if value.EstablishmentState == "not_measurable" && old.EstablishmentState != "not_measurable" {
		value.EstablishmentState = old.EstablishmentState
		value.EstablishmentMS = old.EstablishmentMS
		value.Evidence = old.Evidence
	}
	if old.StartedAt.Before(value.StartedAt) {
		value.StartedAt = old.StartedAt
	}
	return &value
}

// MergeAcceptedFlow applies the evidence a connection already established to a newer
// observation before it is persisted as the current fact. The log job
// uses this pure fold before storing the next current fact.
func MergeAcceptedFlow(previous, next *config.FlowFact) *config.FlowFact {
	if next == nil {
		return previous
	}
	value := *next
	if previous == nil || previous.Key == "" {
		return &value
	}
	if value.Phase == "" {
		value.Phase = previous.Phase
	}
	if previous.ResponseKnown && !value.ResponseKnown {
		value.ResponseKnown = true
		value.ResponseMS = previous.ResponseMS
	}
	if value.Direction != "blocked" && previous.RouteID != "" && previous.Direction != "unknown" {
		value.RouteID = previous.RouteID
		value.RouteName = previous.RouteName
		value.Direction = previous.Direction
		if previous.Rule != "" {
			value.Rule = previous.Rule
			value.RuleSource = previous.RuleSource
		}
	}
	if previous.ApplicationID != "" {
		value.ApplicationID = previous.ApplicationID
		value.Application = previous.Application
		value.Process = previous.Process
	}
	if previous.Domain != "" {
		value.Domain = previous.Domain
	}
	if previous.IP != "" && value.IP == "" {
		value.IP = previous.IP
		value.IPSource = previous.IPSource
	}
	return &value
}

// ApplyLogObservation updates one frozen connection summary from one observation.
// It is the single implementation of the cumulative-byte and gap rules; the
// SQLite log job persists the returned value as JSON.
func ApplyLogObservation(summary config.LogConnectionSummary, o config.Observation, gap bool, clearedAt time.Time) (config.LogConnectionSummary, config.ConnectionMetrics) {
	if o.Flow == nil {
		return summary, summary.Metrics
	}
	fresh := summary.Fact.Key == ""
	if fresh {
		summary.Fact = *o.Flow
		summary.Metrics = config.ConnectionMetrics{BytesState: "unknown", Reason: "missing_start", BytesFrom: o.At}
		if o.Kind == "started" && !gap && !o.Flow.StartedAt.Before(clearedAt) {
			summary.Metrics.BytesState = "complete"
			summary.Metrics.Reason = ""
			summary.Metrics.BytesFrom = o.Flow.StartedAt
		}
		if o.Flow.StartedAt.Before(clearedAt) {
			summary.Metrics.BytesState = "partial"
			summary.Metrics.Reason = "after_clear"
		}
	}
	summary.Fact = *MergeFlow(&summary.Fact, o.Flow)
	if gap {
		at := summary.Metrics.AsOf
		summary.Fact.StateUnknown = true
		summary.Fact.ObservedUntil = &at
		summary.Metrics.BytesState = "partial"
		summary.Metrics.Reason = "observation_gap"
	}
	if o.Upload > 0 || o.Download > 0 {
		if summary.Metrics.BytesState == "unknown" {
			summary.Metrics.BytesState = "partial"
		}
		skip := fresh && o.Flow.StartedAt.Before(clearedAt)
		if !skip {
			if math.MaxUint64-summary.Metrics.Upload < o.Upload || math.MaxUint64-summary.Metrics.Download < o.Download {
				summary.Metrics.BytesState = "partial"
				summary.Metrics.Reason = "counter_overflow"
			} else {
				summary.Metrics.Upload += o.Upload
				summary.Metrics.Download += o.Download
			}
		}
	}
	if o.At.After(summary.Metrics.AsOf) {
		summary.Metrics.AsOf = o.At
	}
	summary.LastSequence = o.Sequence
	return summary, summary.Metrics
}

// MarkLogGap marks one still-open summary as unknown from its last known time.
// Closed connections keep their recorded result.
func MarkLogGap(summary config.LogConnectionSummary) config.LogConnectionSummary {
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

// CheckpointLogEvent attaches the summary's current metrics to an event and records
// the event as the connection's latest visible evidence. The caller only passes a
// summary that exists; e is updated in place.
func CheckpointLogEvent(summary config.LogConnectionSummary, e *config.HistoryEvent) config.LogConnectionSummary {
	if e.Flow == nil || summary.Fact.Key == "" {
		return summary
	}
	copy := summary.Metrics
	e.Metrics = &copy
	summary.LastEventID = e.ID
	if e.At.After(summary.LastLogAt) {
		summary.LastLogAt = e.At
	}
	return summary
}

// LimitEventRaw keeps at most eight raw lines, as the legacy event store did.
func LimitEventRaw(e *config.HistoryEvent) {
	if len(e.Raw) > 8 {
		e.Raw = e.Raw[:8]
		e.Truncated = true
	}
}
