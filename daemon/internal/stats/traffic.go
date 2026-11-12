package stats

import (
	"crypto/sha256"
	"encoding/hex"
	"pax-connect/daemon/internal/config"
	"sort"
	"strings"
	"time"
)

// FlowWindow carries bytes actually transferred inside one frozen window.
type FlowWindow struct {
	Fact             config.FlowFact
	Upload, Download uint64
	// LastTransfer is the latest time bytes moved inside the window; zero when none were recorded.
	LastTransfer time.Time
}

// ActiveAt is the latest evidence of activity inside [from, until]: a transfer,
// the start, or the close. Zero when the flow only overlaps the window silently.
func (v FlowWindow) ActiveAt(from, until time.Time) time.Time {
	latest := time.Time{}
	for _, t := range []time.Time{v.LastTransfer, v.Fact.StartedAt, closedAt(v.Fact)} {
		if t.After(from) && !t.After(until) && t.After(latest) {
			latest = t
		}
	}
	return latest
}
func closedAt(f config.FlowFact) time.Time {
	if f.ClosedAt == nil {
		return time.Time{}
	}
	return *f.ClosedAt
}

// TrafficGroup is an object and actual exit projection of one connection set.
type TrafficGroup struct {
	ID, ObjectID, Name, RouteID, RouteName, Direction string
	Upload, Download, Slowest                         uint64
	LastActive                                        time.Time
	EstablishmentKnown                                bool
	Flows                                             []FlowWindow
}

// AggregateTraffic groups the same flow set without summing across dimensions.
func AggregateTraffic(flows []FlowWindow, dimension, direction, search string, from, until time.Time) []TrafficGroup {
	groups := map[string]*TrafficGroup{}
	for _, v := range flows {
		f := v.Fact
		if !TrafficMatches(f, dimension, direction, search, from, until) {
			continue
		}
		object, name := trafficObject(f, dimension)

		sum := sha256.Sum256([]byte(dimension + "\x00" + object + "\x00" + f.Direction + "\x00" + f.RouteID))
		switch dimension {
		case "dns", "unknown":
			sum = sha256.Sum256([]byte(dimension + "\x00" + object))
		}
		key := hex.EncodeToString(sum[:16])
		g := groups[key]
		if g == nil {
			g = &TrafficGroup{ID: key, ObjectID: object, Name: name, RouteID: f.RouteID, RouteName: f.RouteName, Direction: f.Direction}
			groups[key] = g
		}
		g.Upload += v.Upload
		g.Download += v.Download
		g.Flows = append(g.Flows, v)
		if at := v.ActiveAt(from, until); at.After(g.LastActive) {
			g.LastActive = at
		}
		if f.EstablishmentState == "ready" && f.StartedAt.After(from) && !f.StartedAt.After(until) {
			g.EstablishmentKnown = true
			g.Slowest = max(g.Slowest, f.EstablishmentMS)
		}
	}
	out := make([]TrafficGroup, 0, len(groups))
	for _, g := range groups {
		sort.Slice(g.Flows, func(i, j int) bool {
			a, b := g.Flows[i].Fact, g.Flows[j].Fact
			if a.StartedAt.Equal(b.StartedAt) {
				return a.Key < b.Key
			}
			return a.StartedAt.After(b.StartedAt)
		})
		if dimension == "dns" {
			g.RouteID = ""
			g.RouteName = ""
			g.Direction = "unknown"
		}
		if dimension == "unknown" {
			for _, flow := range g.Flows {
				if flow.Fact.RouteID != g.RouteID || flow.Fact.Direction != g.Direction {
					g.Direction = "mixed"
					g.RouteID = ""
					g.RouteName = "多个连接去向，见详情"
					break
				}
			}
		}
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !a.LastActive.Equal(b.LastActive) {
			return a.LastActive.After(b.LastActive)
		}
		if a.Upload+a.Download == b.Upload+b.Download {
			return a.ID < b.ID
		}
		return a.Upload+a.Download > b.Upload+b.Download
	})
	return out
}

// TrafficMatches applies the same pure window/filter predicate without allocating a group or hashing.
func TrafficMatches(f config.FlowFact, dimension, direction, search string, from, until time.Time) bool {

	source := config.ClassifyTrafficSource(f)
	switch dimension {
	case "system":
		if source.Kind != "system" && source.Kind != "tool" {
			return false
		}
	case "application":
		if source.Kind != "app" {
			return false
		}
	case "dns":
		if source.Kind != "dns" {
			return false
		}
	case "unknown":
		if source.Kind != "unknown" {
			return false
		}
	default:
		if source.Kind == "dns" {
			return false
		}
	}
	if f.ObservedUntil != nil && !f.ObservedUntil.After(from) {
		return false
	}
	if !f.StartedAt.Before(until) || f.ClosedAt != nil && !f.ClosedAt.After(from) {
		return false
	}
	if dimension != "dns" && direction != "" && direction != "all" && f.Direction != direction {
		return false
	}
	if search == "" {
		return true
	}
	_, name := trafficObject(f, dimension)
	return strings.Contains(strings.ToLower(strings.Join([]string{name, f.Process, f.Domain, f.IP, f.RouteName, f.Rule, f.RuleSource}, " ")), strings.ToLower(search))
}
func trafficObject(f config.FlowFact, dimension string) (string, string) {

	if dimension == "application" || dimension == "system" || dimension == "unknown" || dimension == "dns" {
		source := config.ClassifyTrafficSource(f)
		return source.ID, source.Name
	}
	if dimension == "ip" {
		if f.IP != "" {
			return f.IP, f.IP
		}
		return "unknown", "目标 IP 未采集"
	}
	return config.TrafficTarget(f)
}
