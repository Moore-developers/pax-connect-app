package controller

import (
	"errors"
	"net/netip"

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
