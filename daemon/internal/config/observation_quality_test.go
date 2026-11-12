package config

import "testing"

func TestDNSClassificationRequiresRecordedAction(t *testing.T) {
	for _, f := range []FlowFact{{Port: 53, Direction: "unknown"}, {Port: 53, Purpose: "access", Transport: "udp"}, {Rule: `{"Package":"Custom","Method":"domain","Target":"dns.example.com"}`}} {
		if IsDNSControl(f) {
			t.Fatal("guessed DNS from port/name", f)
		}
	}
	for _, f := range []FlowFact{{Purpose: "dns"}, {Rule: `{"Package":"Geo 基础包","Method":"builtin","Target":"DNS 查询"}`}, {Rule: "builtin=DNS 查询", RuleSource: "Pax 包：Geo 基础包"}} {
		if !IsDNSControl(f) {
			t.Fatal("ignored explicit DNS evidence", f)
		}
	}
}
