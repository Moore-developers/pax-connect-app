package controller

import (
	"fmt"
	"sort"
	"strings"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

// Rule evidence belongs to the observed connection, never the current policy.
type aggregateBasis struct {
	Rule, Source, RouteName string
	Connections             uint64
	Observations            uint64
	Contribution            *ruleContribution    `json:",omitempty"`
	MatchTracked            bool                 `json:",omitempty"`
	LatestMatch             *trafficRoutingMatch `json:",omitempty"`
	MatchUnknownUntil       time.Time            `json:",omitempty"`
}

func flowAggregateBases(f config.FlowFact, starts uint64) map[string]aggregateBasis {
	return map[string]aggregateBasis{f.Rule + "\x00" + f.RuleSource: {Rule: f.Rule, Source: f.RuleSource, RouteName: f.RouteName, Connections: starts, Observations: 1}}
}
func mergeRoutingBases(to, from map[string]aggregateBasis) map[string]aggregateBasis {
	if to == nil {
		to = map[string]aggregateBasis{}
	}
	for key, entry := range from {
		old, exists := to[key]
		if exists {
			if old.Contribution == nil || entry.Contribution == nil {
				entry.Contribution = nil
			} else {
				entry.Contribution = mergeRuleContribution(old.Contribution, entry.Contribution)
			}
		}
		entry.LatestMatch = newerRoutingMatch(old.LatestMatch, entry.LatestMatch)
		if old.MatchUnknownUntil.After(entry.MatchUnknownUntil) {
			entry.MatchUnknownUntil = old.MatchUnknownUntil
		}
		entry.MatchTracked = old.MatchTracked && entry.MatchTracked || !exists && entry.MatchTracked
		entry.Connections += to[key].Connections
		entry.Observations += to[key].Observations
		to[key] = entry
	}
	return to
}
func mergeAggregateBases(to *aggregateBucket, from map[string]aggregateBasis) {
	if len(from) > 0 {
		to.Bases = mergeRoutingBases(to.Bases, from)
	}
}
func applyAggregateBases(row *pb.TrafficRow, bases map[string]aggregateBasis) {
	if len(bases) == 0 {
		bases = flowAggregateBases(config.FlowFact{RouteName: row.RouteName}, row.Connections)
	}
	lines := map[string]bool{}
	keys := make([]string, 0, len(bases))
	for key := range bases {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		b := bases[key]
		row.Bases = append(row.Bases, &pb.TrafficBasis{Rule: b.Rule, Source: b.Source, RouteName: b.RouteName, Connections: b.Connections})
		lines[config.TrafficBasisLine(b.Rule, b.Source)] = true
	}
	names := make([]string, 0, len(lines))
	for line := range lines {
		names = append(names, line)
	}
	sort.Strings(names)
	row.Basis = strings.Join(names[:min(2, len(names))], "\n")
	if len(names) > 2 {
		row.Basis += fmt.Sprintf("\n另 %d 项", len(names)-2)
	}
}
