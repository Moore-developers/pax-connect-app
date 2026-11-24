package controller

import (
	"sort"

	"pax-connect/daemon/internal/config"
)

// nextAlertEpisodes is pure apart from injected ID creation. Incomplete windows
// can prove an excess, but cannot prove recovery and re-arm a notice.
func nextAlertEpisodes(old map[string]config.TrafficAlertEpisode, windows []alertWindow, newID func() (string, error)) (map[string]config.TrafficAlertEpisode, []config.TrafficAlertNotice, error) {
	next := make(map[string]config.TrafficAlertEpisode, len(old))
	for k, v := range old {
		next[k] = v
	}
	sources := map[string]bool{}
	complete := true
	for _, w := range windows {
		complete = complete && !w.partial
		for key := range w.sources {
			sources[key] = true
		}
	}
	for key := range old {
		if len(key) > 2 {
			sources[key[:len(key)-2]] = true
		}
	}
	var notices []config.TrafficAlertNotice
	for source := range sources {
		for _, direction := range []string{"upload", "download"} {
			suffix := ":u"
			if direction == "download" {
				suffix = ":d"
			}
			key := source + suffix
			e, exists := next[key]
			var chosen *alertWindow
			var daily *alertWindow
			latestStillActive := false
			for i := range windows {
				w := &windows[i]
				s := w.sources[source]
				if s == nil {
					continue
				}
				amount, threshold := s.up, w.rule.Upload
				if direction == "download" {
					amount, threshold = s.down, w.rule.Download
				}
				if amount < threshold {
					continue
				}
				if e.Latest.WindowSeconds == w.seconds {
					latestStillActive = true
				}
				if chosen == nil || w.seconds == 86400 || w.seconds == 300 && chosen.seconds != 86400 {
					chosen = w
				}
				if w.seconds == 86400 {
					daily = w
				}
			}
			if chosen == nil {
				if complete {
					delete(next, key)
				}
				continue
			}
			if exists && !e.DayNotified && daily != nil {
				chosen = daily
			} else if exists {
				chosen = nil
			}
			if chosen != nil {
				w := chosen
				s := w.sources[source]
				id, err := newID()
				if err != nil {
					return nil, nil, err
				}
				n := config.TrafficAlertNotice{ID: id, Key: key, Source: s.name, SourceKind: s.kind, Direction: direction, WindowSeconds: w.seconds, Bytes: s.up, Threshold: w.rule.Upload, Until: w.until, Partial: w.partial}
				if direction == "download" {
					n.Bytes, n.Threshold = s.down, w.rule.Download
				}
				var leading uint64
				for route, r := range s.routes {
					amount := r.up
					if direction == "download" {
						amount = r.down
					}
					if amount == 0 {
						continue
					}
					n.RouteCount++
					if amount > leading || amount == leading && route < n.RouteID {
						leading = amount
						n.RouteID = route
						n.RouteName = r.name
					}
				}
				e.Latest = n
				e.DayNotified = e.DayNotified || w.seconds == 86400
				next[key] = e
				latestStillActive = true
			}
			if latestStillActive {
				notices = append(notices, e.Latest)
			}
		}
	}
	sort.Slice(notices, func(i, j int) bool { return notices[i].Key < notices[j].Key })
	return next, notices, nil
}
