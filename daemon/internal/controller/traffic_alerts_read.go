package controller

import (
	"context"
	"errors"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

type alertTransfer struct {
	up, down uint64
	name     string
}
type alertSource struct {
	name, kind string
	up, down   uint64
	routes     map[string]alertTransfer
}
type alertWindow struct {
	seconds uint32
	rule    config.TrafficAlertRule
	until   time.Time
	partial bool
	sources map[string]*alertSource
}
type alertWindowCache struct {
	until           time.Time
	revision        uint64
	trafficRevision uint64
	pendingThrough  historystore.Cursor
	windows         []alertWindow
}

func enabledAlertWindows(p config.TrafficAlertSettings) []alertWindow {
	medium := p.Hour
	if p.MediumSeconds == 7200 {
		medium = p.TwoHours
	}
	var out []alertWindow
	for _, v := range []struct {
		seconds uint32
		rule    config.TrafficAlertRule
	}{{300, p.Short}, {p.MediumSeconds, medium}, {86400, p.Day}} {
		if v.rule.Enabled {
			out = append(out, alertWindow{seconds: v.seconds, rule: v.rule})
		}
	}
	return out
}

func alertWindows(p config.TrafficAlertSettings, at time.Time) []alertWindow {
	windows := enabledAlertWindows(p)
	for i := range windows {
		w := &windows[i]
		end := at.Truncate(trafficBucket5m)
		if w.seconds == 300 {
			end = at.Truncate(trafficBucket5s)
		}
		w.until = end
		w.sources = map[string]*alertSource{}
	}
	return windows
}

// readAlertWindows streams source dimensions once per horizon. Domain/IP/route
// copies are deliberately excluded, so a transfer contributes exactly once.
func (c *Controller) readAlertWindows(ctx context.Context, p config.TrafficAlertSettings, cache *alertWindowCache) ([]alertWindow, error) {
	at := c.now()
	var proof *coverageSpan
	if c.ingest != nil {
		proof = c.ingest.coverage.Load()
	}
	if proof == nil || !proof.collecting || at.Sub(proof.last) > trafficPollSpacing {
		return nil, errors.New("collection_interrupted")
	}
	end := minTime(at, proof.last)
	windows := alertWindows(p, end)
	if len(windows) == 0 {
		return windows, nil
	}
	snap, err := c.trafficSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = snap.read.Close() }()
	if snap.meta.Error != "" {
		return nil, errors.New(snap.meta.Error)
	}
	longEnd := end.Truncate(trafficBucket5m)
	var pendingThrough historystore.Cursor
	if len(snap.entries) > 0 {
		entry := snap.entries[len(snap.entries)-1]
		pendingThrough = historystore.Cursor{Session: entry.session, Seq: entry.seq}
	}
	useCache := cache.until.Equal(longEnd) && cache.revision == p.Revision && cache.trafficRevision == snap.meta.TrafficRevision && cache.pendingThrough == pendingThrough
	var earliest time.Time
	for i := range windows {
		w := &windows[i]
		if w.seconds != 300 && useCache {
			for _, old := range cache.windows {
				if old.seconds == w.seconds && !old.partial {
					*w = old
					break
				}
			}
		}
		from := w.until.Add(-time.Duration(w.seconds) * time.Second)
		if earliest.IsZero() || from.Before(earliest) {
			earliest = from
		}
	}
	coverage, err := snap.coverageWithPoll(ctx, earliest, end, proof)
	if err != nil {
		return nil, err
	}
	needLong := false
	for i := range windows {
		w := &windows[i]
		cached := false
		if w.seconds != 300 && useCache {
			for _, old := range cache.windows {
				if old.seconds == w.seconds && !old.partial {
					cached = true
					break
				}
			}
		}
		if cached {
			continue
		}
		w.partial = snap.compact.Partial || !fullAggregateCoverage(coverage, w.until.Add(-time.Duration(w.seconds)*time.Second), w.until)
		if w.seconds != 300 {
			needLong = true
		}
	}
	dimensions := []string{"application", "system", "unknown"}
	projectionBytes := 0
	add := func(v aggregateBucket, short bool) error {
		id := v.Identity
		if id.Kind == "legacy" || id.Kind == "dns" || v.Upload == 0 && v.Download == 0 {
			return nil
		}
		key := id.Kind + ":" + id.Object
		if id.Kind == "unknown" {
			key = "unknown:" + id.Route
		}
		for i := range windows {
			w := &windows[i]
			if (w.seconds == 300) != short || v.At.Before(w.until.Add(-time.Duration(w.seconds)*time.Second)) || !v.At.Before(w.until) {
				continue
			}
			if !short && useCache {
				cached := false
				for _, old := range cache.windows {
					if old.seconds == w.seconds && !old.partial {
						cached = true
						break
					}
				}
				if cached {
					continue
				}
			}
			s := w.sources[key]
			if s == nil {
				projectionBytes += 256 + len(key) + len(id.Name)
				s = &alertSource{name: storedAggregateName(id), kind: id.Kind, routes: map[string]alertTransfer{}}
				w.sources[key] = s
			}
			s.up += v.Upload
			s.down += v.Download
			r, exists := s.routes[id.Route]
			if !exists {
				projectionBytes += 128 + len(id.Route) + len(id.RouteName)
			}
			if projectionBytes > 16<<20 {
				return errors.New("history_query_busy")
			}
			r.up += v.Upload
			r.down += v.Download
			r.name = id.RouteName
			s.routes[id.Route] = r
		}
		return nil
	}
	read := func(recent bool, from, until time.Time) error {
		return snap.read.Rows(ctx, recent, from, until, dimensions, func(row historystore.TrafficRow) error {
			v, e := decodeTrafficRow(row)
			if e != nil {
				return e
			}
			v.Identity = projectStoredIdentity(v.Identity)
			return add(v, recent)
		})
	}
	shortEnabled := p.Short.Enabled
	if shortEnabled {
		if err = read(true, end.Truncate(trafficBucket5s).Add(-5*time.Minute), end.Truncate(trafficBucket5s)); err != nil {
			return nil, err
		}
	}
	if needLong {
		if err = read(false, earliest.Truncate(trafficBucket5m), longEnd); err != nil {
			return nil, err
		}
	}
	for _, v := range pendingBuckets(snap.entries, earliest, end) {
		if v.Identity.Dimension != "application" && v.Identity.Dimension != "system" && v.Identity.Dimension != "unknown" {
			continue
		}
		if err = add(v, true); err != nil {
			return nil, err
		}
		if err = add(v, false); err != nil {
			return nil, err
		}
	}
	cache.until, cache.revision, cache.windows = longEnd, p.Revision, nil
	cache.trafficRevision, cache.pendingThrough = snap.meta.TrafficRevision, pendingThrough
	for _, w := range windows {
		if w.seconds != 300 {
			cache.windows = append(cache.windows, w)
		}
	}
	return windows, nil
}
