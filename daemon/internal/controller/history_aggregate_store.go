package controller

import (
	"crypto/sha256"
	"encoding/hex"

	"math"
	"strings"
	"time"

	"pax-connect/daemon/internal/config"
)

// Aggregate data stays in the existing history database and observation transaction.
// Counts are new connections, not summed per-bucket active-connection cardinality.
type aggregateIdentity struct {
	Dimension, Object, Name, Kind, Route, RouteName, Direction string
	Search                                                     string
	RulePackage                                                string
}

type aggregateBucket struct {
	LegacyGap                             bool `json:",omitempty"`
	Bases                                 map[string]aggregateBasis
	MutationAt                            time.Time
	Identity                              aggregateIdentity
	At                                    time.Time
	Upload, Download, Connections, Failed uint64
	Latency                               map[int]uint64 `json:",omitempty"`
	Latest, Maximum                       uint64
	LatestAt                              time.Time
}

func aggregateIdentities(f config.FlowFact) []aggregateIdentity {
	source := config.ClassifyTrafficSource(f)
	base := aggregateIdentity{Route: f.RouteID, RouteName: f.RouteName, Direction: f.Direction, Kind: source.Kind, Search: strings.Join([]string{f.Domain, f.IP, f.Process, f.Application, f.Rule}, " ")}
	result := []aggregateIdentity{}
	add := func(dimension, object, name string) {
		v := base
		v.Dimension = dimension
		v.Object = object
		v.Name = name
		result = append(result, v)
	}
	if source.Kind == "app" {
		add("application", source.ID, source.Name)
	}
	if source.Kind == "system" || source.Kind == "tool" {
		add("system", source.ID, source.Name)
	}
	if source.Kind == "unknown" {
		add("unknown", "unknown", source.Name)
	}
	if source.Kind == "dns" {
		add("dns", "dns", "域名解析")
		return result
	}
	id, name := config.TrafficTarget(f)
	add("domain", id, name)
	if f.IP != "" {
		add("ip", f.IP, f.IP)
	}
	add("route", f.RouteID, f.RouteName)
	return result
}
func aggregateID(v aggregateIdentity) string {
	// Name is presentation metadata; renaming an object must not split its totals.
	raw := strings.Join([]string{v.Dimension, v.Object, v.Kind, v.Route, v.Direction}, "\x00")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:16])
}
func latencyBin(ms uint64) int {
	if ms == 0 {
		return 0
	}
	// Fixed logarithmic bounds, at most 25% relative bucket width. The terminal
	// bucket is unbounded and quantiles from it are clamped to the actual maximum.
	bound := 1.0
	for i := 1; i < 64; i++ {
		if float64(ms) <= math.Ceil(bound) {
			return i
		}
		bound *= 1.25
	}
	return 64
}
func latencyBound(bin int) uint64 {
	if bin == 0 {
		return 0
	}
	return uint64(math.Ceil(math.Pow(1.25, float64(bin-1))))
}
func mergeHistogram(to map[int]uint64, from map[int]uint64) {
	for k, v := range from {
		to[k] += v
	}
}
func histogramCount(h map[int]uint64) uint64 {
	var n uint64
	for _, v := range h {
		n += v
	}
	return n
}
func histogramQuantile(h map[int]uint64, percentile, maximum uint64) uint64 {
	n := histogramCount(h)
	if n == 0 {
		return 0
	}
	rank := (n*percentile + 99) / 100
	var seen uint64
	for i := 0; i <= 64; i++ {
		seen += h[i]
		if seen >= rank {
			return min(latencyBound(i), maximum)
		}
	}
	return maximum
}
