package controller

import (
	"errors"
	"net/netip"
	"sort"
	"strings"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

func validateLogCriteria(q *pb.LogQuery) error {
	if !oneOf(q.Scope, "", "access", "system") {
		return errors.New("invalid_filter")
	}
	if !oneOf(q.Quick, "", "all", "problems", "slow", "blocked") {
		return errors.New("invalid_filter")
	}
	c := q.GetCriteria()
	if c == nil {
		return nil
	}
	for _, v := range []string{c.Application, c.Domain, c.TargetIp, c.Route, c.RulePackage, c.DnsAnswerIp} {
		if len(v) > 253 {
			return errors.New("invalid_filter")
		}
	}
	if c.Port > 65535 || !oneOf(c.DomainMode, "", "contains", "exact", "suffix") {
		return errors.New("invalid_filter")
	}
	for _, group := range []struct{ values, allowed []string }{{c.Kinds, []string{"access", "dns", "operation", "diagnostic"}}, {c.Levels, []string{"info", "warning", "error"}}, {c.Transports, []string{"tcp", "udp"}}, {c.Directions, []string{"direct", "proxy", "dns", "blocked", "unknown"}}} {
		if len(group.values) > 8 {
			return errors.New("invalid_filter")
		}
		for _, v := range group.values {
			if !oneOf(v, group.allowed...) {
				return errors.New("invalid_filter")
			}
		}
	}
	for _, v := range []string{c.TargetIp, c.DnsAnswerIp} {
		if v != "" {
			if _, e := netip.ParseAddr(v); e != nil {
				if _, e = netip.ParsePrefix(v); e != nil {
					return errors.New("invalid_filter")
				}
			}
		}
	}
	if c.Domain != "" && oneOf(c.DomainMode, "exact", "suffix") && config.MainDomain(c.Domain) == "" {
		return errors.New("invalid_filter")
	}
	return nil
}
func oneOf(v string, values ...string) bool {
	for _, a := range values {
		if a == v {
			return true
		}
	}
	return false
}
func ipFilter(ip, filter string) bool {
	if filter == "" {
		return true
	}
	address, e := netip.ParseAddr(ip)
	if e != nil {
		return false
	}
	if prefix, e := netip.ParsePrefix(filter); e == nil {
		return prefix.Contains(address.Unmap())
	}
	wanted, e := netip.ParseAddr(filter)
	return e == nil && address.Unmap() == wanted.Unmap()
}
func criteriaMatches(v *pb.LogRecord, q *pb.LogQuery) bool {
	c := q.GetCriteria()
	f := v.GetFlow()
	kind := v.Kind
	if f != nil {
		kind = "access"
		if f.Purpose == "dns" {
			kind = "dns"
		}
	}
	latency, known := uint64(0), false
	if f != nil {
		latency, known = config.FlowLatency(config.FlowFact{ResponseKnown: f.ResponseKnown, ResponseMS: f.ResponseMs, EstablishmentState: f.EstablishmentState, EstablishmentMS: f.EstablishmentMs, Evidence: f.Evidence})
	}
	switch q.Quick {
	case "problems":
		if v.Level != pb.LogLevel_LOG_LEVEL_ERROR && v.Level != pb.LogLevel_LOG_LEVEL_WARNING && (f == nil || !f.StateUnknown) {
			return false
		}
	case "blocked":
		if f == nil || f.Direction != "blocked" && f.Failure != "rule_rejected" {
			return false
		}
	case "slow":
		threshold := uint64(1000)
		if c.GetMinLatencyMs() > 0 {
			threshold = c.MinLatencyMs
		}
		if kind != "access" || !known || latency < threshold {
			return false
		}
	}
	if c == nil {
		return true
	}
	if len(c.Kinds) > 0 && !oneOf(kind, c.Kinds...) {
		return false
	}
	level := map[pb.LogLevel]string{pb.LogLevel_LOG_LEVEL_INFO: "info", pb.LogLevel_LOG_LEVEL_WARNING: "warning", pb.LogLevel_LOG_LEVEL_ERROR: "error"}[v.Level]
	if len(c.Levels) > 0 && !oneOf(level, c.Levels...) {
		return false
	}
	needsFlow := c.Application != "" || c.Domain != "" || c.TargetIp != "" || len(c.Transports) > 0 || c.Port > 0 || len(c.Directions) > 0 || c.Route != "" || c.RulePackage != "" || c.MinLatencyMs > 0 || c.DnsAnswerIp != ""
	if f == nil {
		return !needsFlow
	}
	if c.Application != "" && !strings.Contains(strings.ToLower(f.Application+" "+f.ApplicationId), strings.ToLower(c.Application)) {
		return false
	}
	if c.Domain != "" {
		host := strings.ToLower(strings.TrimSuffix(f.Domain, "."))
		target := strings.ToLower(strings.TrimSuffix(c.Domain, "."))
		switch c.DomainMode {
		case "exact":
			if host != target {
				return false
			}
		case "suffix":
			if host != target && !strings.HasSuffix(host, "."+target) {
				return false
			}
		default:
			if !strings.Contains(host, target) {
				return false
			}
		}
	}
	if c.TargetIp != "" && (kind == "dns" || !ipFilter(f.Ip, c.TargetIp)) {
		return false
	}
	if len(c.Transports) > 0 && !oneOf(f.Transport, c.Transports...) {
		return false
	}
	if c.Port > 0 && f.Port != c.Port {
		return false
	}
	if len(c.Directions) > 0 && !oneOf(f.Direction, c.Directions...) {
		return false
	}
	if c.Route != "" && f.RouteId != c.Route && f.RouteName != c.Route {
		return false
	}
	pack, _ := config.TrafficRuleDescription(f.Rule, f.RuleSource)
	if c.RulePackage != "" && pack != c.RulePackage {
		return false
	}
	if c.MinLatencyMs > 0 && (kind != "access" || !known || latency < c.MinLatencyMs) {
		return false
	}
	if c.DnsAnswerIp != "" {
		matched := false
		for _, ip := range f.GetDns().GetAnswers() {
			if ipFilter(ip, c.DnsAnswerIp) {
				matched = true
				break
			}
		}
		if kind != "dns" || !matched {
			return false
		}
	}
	return true
}
func logOptions(records []*pb.LogRecord) *pb.LogOptions {
	apps, routes, rules := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, r := range records {
		if f := r.Flow; f != nil {
			if f.Application != "" {
				apps[f.Application] = true
			}
			if f.RouteName != "" {
				routes[f.RouteName] = true
			}
			p, _ := config.TrafficRuleDescription(f.Rule, f.RuleSource)
			if p != "" {
				rules[p] = true
			}
		}
	}
	list := func(m map[string]bool) []string {
		out := make([]string, 0, len(m))
		for v := range m {
			out = append(out, v)
		}
		sort.Strings(out)
		if len(out) > 500 {
			out = out[:500]
		}
		return out
	}
	return &pb.LogOptions{Applications: list(apps), Routes: list(routes), RulePackages: list(rules)}
}
