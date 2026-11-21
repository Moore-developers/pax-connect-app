package telemetry_test

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/telemetry"
)

func TestFixedRouteProtectionEventsIncludeFaultPendingAndRecovery(t *testing.T) {
	at := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct{ code, title, result string }{
		{"fixed_route_unavailable", "指定线路 A 不可用，已确认关闭 2 条连接", "failed"},
		{"fixed_route_pending", "指定线路 A 已拦截，连接关闭待确认", "pending"},
		{"fixed_route_recovered", "指定线路 A 已恢复，新连接可再次使用", "completed"},
	} {
		t.Run(test.code, func(t *testing.T) {
			for _, level := range []string{"info", "warn"} {
				event := telemetry.ProductEvent(config.Observation{Session: "core-fixture", Sequence: 3, At: at, Kind: "route_protection", Code: test.code, Raw: test.title, Level: level})
				if event == nil || event.System == nil {
					t.Fatalf("protection event disappeared at %s", level)
				}
				if event.Title != test.title || event.ID != "core-fixture:3" || !event.At.Equal(at) || event.Source != "Pax" || event.Flow != nil || len(event.Raw) != 0 || event.Result != test.result {
					t.Fatalf("product event lost its evidence: %+v", event)
				}
				if event.System.Type != "runtime."+test.code || event.System.Name != event.Title || event.System.Result != test.result || event.System.Category.Key != "runtime" || event.System.Category.Name != "网络与后台" || config.ValidateSystemEvent(event.System) != nil {
					t.Fatalf("invalid runtime event: %+v", event.System)
				}
				if event.Level != map[string]string{"info": "info", "warn": "warning"}[level] {
					t.Fatalf("level = %s", event.Level)
				}
			}
		})
	}
	for _, code := range []string{"", "fixed_route_future", "other_free_text"} {
		if event := telemetry.ProductEvent(config.Observation{Kind: "route_protection", Code: code, Raw: "不应成为系统事件", Level: "warn"}); event != nil {
			t.Fatalf("unknown protection code became an event: %+v", event)
		}
	}
}

func TestFixedRouteProtectionEventScrubsCredentialsAndBoundsTitle(t *testing.T) {
	input := "指定线路 https://user:private-pass@node.example/path?token=private-token 已恢复 token=private-token Authorization: Bearer private-bearer"
	event := telemetry.ProductEvent(config.Observation{Kind: "route_protection", Code: "fixed_route_recovered", Raw: input, Level: "info"})
	if event == nil {
		t.Fatal("missing recovery")
	}
	for _, secret := range []string{"private-pass", "private-token", "private-bearer", "user:"} {
		if strings.Contains(event.Title, secret) {
			t.Fatalf("credential survived in title: %s", event.Title)
		}
	}
	if len(event.Raw) != 0 || !strings.Contains(event.Title, "https://node.example/path") {
		t.Fatalf("unexpected scrubbed evidence: %+v", event)
	}
	event = telemetry.ProductEvent(config.Observation{Kind: "route_protection", Code: "fixed_route_unavailable", Raw: strings.Repeat("线", 300)})
	if event == nil || len(event.Title) > 256 || len(event.Title) < 253 || !utf8.ValidString(event.Title) || !event.Truncated || config.ValidateSystemEvent(event.System) != nil {
		t.Fatalf("unbounded event: %+v", event)
	}
}
