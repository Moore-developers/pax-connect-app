package stats

import (
	"pax-connect/daemon/internal/config"
	"testing"
	"time"
)

func TestTrafficDoesNotRankRecordedDNSControlAsUnknownApplication(t *testing.T) {
	at := time.Now().Add(-time.Second)
	dns := config.FlowFact{Key: "dns", IP: "192.0.2.53", Port: 53, Direction: "unknown", StartedAt: at,
		Rule: `{"Package":"Geo 基础包","Method":"builtin","Target":"DNS 查询"}`}
	access := config.FlowFact{Key: "access", IP: "192.0.2.1", Port: 443, Direction: "direct", StartedAt: at}
	rows := AggregateTraffic([]FlowWindow{{Fact: dns}, {Fact: access}}, "unknown", "all", "", at.Add(-time.Second), at.Add(time.Second))
	if len(rows) != 1 || len(rows[0].Flows) != 1 || rows[0].Flows[0].Fact.Key != "access" {
		t.Fatalf("DNS control polluted unknown application ranking: %+v", rows)
	}
}
