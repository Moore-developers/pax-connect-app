package controller

import (
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/stats"
)

// retainedConnectionFlow applies retained rejection evidence and hides state after
// the query cutoff. Membership projections share this production attribution rule.
func retainedConnectionFlow(s config.LogConnectionSummary, event config.HistoryEvent, at time.Time) stats.FlowWindow {
	f := s.Fact
	if f.Direction == "unknown" && f.RouteID == "" && f.Failure == "rule_rejected" {
		if event.ID == s.LastEventID {
			rejected := event.Flow
			if rejected != nil && rejected.Key == f.Key && rejected.Direction == "blocked" && rejected.Failure == "rule_rejected" && !event.At.IsZero() && !event.At.After(at) {
				f.Direction, f.RouteID, f.RouteName = rejected.Direction, rejected.RouteID, rejected.RouteName
				f.Rule, f.RuleSource = rejected.Rule, rejected.RuleSource
			}
		}
	}
	if f.ObservedUntil != nil && f.ObservedUntil.After(at) || f.ClosedAt != nil && f.ClosedAt.After(at) {
		f.ClosedAt, f.ObservedUntil = nil, nil
		f.ResponseKnown, f.StateUnknown = false, true
		f.EstablishmentState, f.Failure, f.Phase = "", "", ""
	}
	return stats.FlowWindow{Fact: f, Upload: s.Metrics.Upload, Download: s.Metrics.Download}
}
