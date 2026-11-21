package telemetry

import (
	"strconv"
	"unicode/utf8"

	"pax-connect/daemon/internal/config"
)

// Only the root owner's explicit protection states become product events. These
// messages retain their local display text without accepting arbitrary raw logs.
func fixedRouteEvent(o config.Observation) *config.HistoryEvent {
	var result string
	switch o.Code {
	case "fixed_route_unavailable":
		result = "failed"
	case "fixed_route_pending":
		result = "pending"
	case "fixed_route_recovered":
		result = "completed"
	default:
		return nil
	}
	title := ScrubEventText(o.Raw)
	truncated := len(title) > 256
	if truncated {
		title = title[:256]
		for !utf8.ValidString(title) {
			title = title[:len(title)-1]
		}
	}
	level := o.Level
	if level == "warn" {
		level = "warning"
	}
	if level == "" {
		level = "info"
	}
	return &config.HistoryEvent{
		ID:        o.Session + ":" + strconv.FormatUint(o.Sequence, 10),
		At:        o.At,
		Kind:      o.Kind,
		Title:     title,
		Level:     level,
		Source:    "Pax",
		Result:    result,
		Truncated: truncated,
		System: &config.SystemEvent{
			Type:     "runtime." + o.Code,
			Name:     title,
			Category: config.EventCategory{Key: "runtime", Name: "网络与后台", Symbol: "network"},
			Result:   result,
		},
	}
}
