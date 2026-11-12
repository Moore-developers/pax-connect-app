package telemetry

import (
	"pax-connect/daemon/internal/config"
	"testing"
)

func TestExplicitRejectionDoesNotInheritAPreviousRoute(t *testing.T) {
	for _, previous := range []config.FlowFact{
		{Key: "flow", Direction: "unknown"},
		{Key: "flow", Direction: "proxy", RouteID: "selected", Rule: "final"},
	} {
		blocked := config.FlowFact{Key: "flow", Direction: "blocked", Failure: "rule_rejected", Rule: "builtin=IPv6 流量", RuleSource: "Pax 包：Geo 基础包"}
		next := MergeAcceptedFlow(&previous, &blocked)
		result := MergeFlow(&previous, next)
		if result.Direction != "blocked" || result.RouteID != "" || result.Rule != blocked.Rule || result.RuleSource != blocked.RuleSource {
			t.Fatal("explicit rejection lost", result)
		}
	}
	previous := config.FlowFact{Key: "flow", Direction: "proxy", RouteID: "selected", Rule: "final", ResponseKnown: true, ResponseMS: 18}
	incomplete := config.FlowFact{Key: "flow", Direction: "unknown"}
	result := MergeFlow(&previous, MergeAcceptedFlow(&previous, &incomplete))
	if result.RouteID != "selected" || result.Direction != "proxy" || !result.ResponseKnown || result.ResponseMS != 18 {
		t.Fatal("ordinary missing evidence stopped inheriting accepted route", result)
	}
}
