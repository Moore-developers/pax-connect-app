package controller

import (
	"strings"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/stats"
)

// One index per transaction bucket avoids rescanning accumulated text for every
// observation. The final string is encoded only once, alongside the bucket.
type trafficSearchIndex struct {
	seen  map[string]bool
	parts []string
}

func newTrafficSearchIndex(previous string) *trafficSearchIndex {
	index := &trafficSearchIndex{seen: map[string]bool{}}
	index.add(previous)
	return index
}
func (s *trafficSearchIndex) add(text string) {
	for _, part := range strings.Split(text, "\n") {
		if part != "" && !s.seen[part] {
			s.seen[part] = true
			s.parts = append(s.parts, part)
		}
	}
}

type trafficSearchKey struct{ dimension, object, route, direction string }

func searchKey(i aggregateIdentity) trafficSearchKey {
	key := trafficSearchKey{i.Dimension, i.Object, i.Route, i.Direction}
	if mergedRowDimension(i.Dimension) {
		key.route, key.direction = "", ""
	}
	return key
}

// trafficSearch only chooses complete displayed objects. Time and direction have
// already selected contributions before observe; search never selects byte deltas.
type trafficSearch struct {
	term    string
	matched map[trafficSearchKey]bool
}

func newTrafficSearch(term string) *trafficSearch {
	return &trafficSearch{term: strings.ToLower(term), matched: map[trafficSearchKey]bool{}}
}
func (s *trafficSearch) observe(i aggregateIdentity) {
	if s.term == "" {
		return
	}
	if strings.Contains(strings.ToLower(strings.Join([]string{i.Name, i.Object, i.RouteName, i.Search}, " ")), s.term) {
		s.matched[searchKey(i)] = true
	}
}

// observeBucket also searches rule evidence retained before text indexing existed.
func (s *trafficSearch) observeBucket(v aggregateBucket) {
	s.observe(v.Identity)
	if s.term == "" || s.includes(v.Identity) {
		return
	}
	for _, basis := range v.Bases {
		if strings.Contains(strings.ToLower(strings.Join([]string{basis.Rule, basis.Source, basis.RouteName}, " ")), s.term) {
			s.matched[searchKey(v.Identity)] = true
			return
		}
	}
}
func (s *trafficSearch) includes(i aggregateIdentity) bool {
	return s.term == "" || s.matched[searchKey(i)]
}
func (s *trafficSearch) includesRow(row *pb.TrafficRow, dimension string) bool {
	if row == nil {
		return false
	}
	if dimension == "" {
		dimension = "domain"
	}
	if dimension == "network" {
		dimension = "route"
	}
	if dimension == "application" && (row.Kind == "unknown" || row.SourceKind == "unknown") {
		dimension = "unknown"
	}
	if dimension == "system" && (row.Kind == "dns" || row.SourceKind == "dns") {
		dimension = "dns"
	}
	return s.includes(aggregateIdentity{Dimension: dimension, Object: row.ObjectId, Route: row.RouteId, Direction: row.Direction})
}

func trafficGroupMatchesSearch(g stats.TrafficGroup, term string) bool {
	if term == "" {
		return true
	}
	term = strings.ToLower(term)
	for _, v := range g.Flows {
		f := v.Fact
		if strings.Contains(strings.ToLower(strings.Join([]string{g.Name, g.ObjectID, f.Application, f.Process, f.Domain, f.IP, f.RouteName, f.Rule, f.RuleSource}, " ")), term) {
			return true
		}
	}
	return false
}
