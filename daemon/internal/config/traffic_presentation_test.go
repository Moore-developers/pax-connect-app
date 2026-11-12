package config

import (
	"strings"
	"testing"
)

func TestResponseLatencyDoesNotReuseTransportReady(t *testing.T) {
	if _, known := FlowLatency(FlowFact{EstablishmentState: "ready", EstablishmentMS: 9}); known {
		t.Fatal("transport mislabeled response")
	}
	if ms, known := FlowLatency(FlowFact{EstablishmentState: "response_ready", Evidence: "first_response", EstablishmentMS: 80}); !known || ms != 80 {
		t.Fatal(ms, known)
	}
}
func TestRoutingEvidenceAlignedAndReadable(t *testing.T) {
	l := NewRoutingLibrary()
	l.Packages[0].Rules = []RoutingEntry{{ID: "app", Method: "domain", Target: "example.com", Destination: "direct", Enabled: true}}
	rules, names, err := CompileRoutingEvidence(l, nil, nil, true)
	if err != nil || len(rules) != len(names) {
		t.Fatal(rules, names, err)
	}
	pack, description := TrafficRuleDescription(names[2], "内核规则")
	if pack != "优先规则" || !strings.Contains(description, "example.com") {
		t.Fatal(pack, description)
	}
}

func TestExplicitCustomSourceCannotBeGuessedAsGeo(t *testing.T) {
	for _, rule := range []string{"domain_suffix=geosite-cn.example.com", "domain=geoip-cn.example.com", "domain=ip_is_private.example.com"} {
		pack, description := TrafficRuleDescription(rule, "Pax 包：自定义包")
		if pack != "自定义包" || strings.Contains(description, "直连规则") {
			t.Fatal(pack, description)
		}
	}
}

func TestIPv4OnlyRuleEvidenceNamesItsActualOwner(t *testing.T) {
	rules, labels, err := CompileRoutingEvidence(NewRoutingLibrary(), nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for i, rule := range rules {
		if rule["ip_version"] != float64(6) {
			continue
		}
		found = true
		evidence, ok := RecordedRoutingEvidence(labels[i])
		if !ok || evidence.Package != "Pax 内置规则" || evidence.Method != "builtin" || evidence.Target != "IPv6 流量" {
			t.Fatalf("IPv6 rejection evidence: %+v", evidence)
		}
	}
	if !found {
		t.Fatal("IPv4-only reject rule is missing")
	}
	for _, rule := range []string{
		"builtin=IPv6 流量",
		`{"Package":"Geo 基础包","Method":"builtin","Target":"IPv6 流量"}`,
		`{"Package":"Pax 内置规则","Method":"builtin","Target":"IPv6 流量"}`,
	} {
		for _, source := range []string{"Pax 包：Geo 基础包", "Pax 包：Pax 内置规则"} {
			pack, condition := TrafficRuleDescription(rule, source)
			if pack != "Pax 内置规则" || condition != "IPv6 流量" || TrafficRulePackage(rule, source) != pack || TrafficRuleGroup(rule, source) != pack || TrafficBasisLine(rule, source) != pack {
				t.Errorf("inconsistent projection of %s: %s / %s", rule, pack, condition)
			}
		}
	}
	for _, tc := range []struct{ rule, source, pack string }{
		{"builtin=IPv6 流量", "Pax 包：自定义包", "自定义包"},
		{"builtin=IPv6 流量", "内核规则", "历史规则（未记录包名）"},
		{"builtin=IPv6 流量扩展", "Pax 包：Geo 基础包", "Geo 基础包"},
		{`{"Package":"自定义包","Method":"builtin","Target":"IPv6 流量"}`, "Pax 包：Geo 基础包", "自定义包"},
		{"domain=IPv6 流量", "Pax 包：Geo 基础包", "Geo 基础包"},
		{"ip_is_private", "内核规则", "Geo 基础包"},
		{"source=Google 服务经代理", "Pax 包：geosite-google", "Geo 基础包"},
		{`{"Package":"Geo 基础包","Method":"builtin","Target":"私有网络地址"}`, "Pax 包：Geo 基础包", "Geo 基础包"},
	} {
		if got := TrafficRulePackage(tc.rule, tc.source); got != tc.pack {
			t.Errorf("%s: got %s, want %s", tc.rule, got, tc.pack)
		}
	}
}
