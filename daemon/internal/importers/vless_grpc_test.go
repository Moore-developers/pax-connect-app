package importers

import (
	"strings"
	"testing"
)

const (
	vlessGRPCLink = "vless://11111111-1111-4111-8111-111111111111@edge.example.test"
	vlessGRPCYAML = "  - {name: Test, type: vless, server: edge.example.test, port: 443, uuid: 11111111-1111-4111-8111-111111111111, tls: true, network: grpc"
	vlessGRPCJSON = `{"type":"vless","tag":"Test","server":"edge.example.test","server_port":443,"uuid":"11111111-1111-4111-8111-111111111111","tls":{"enabled":true},"transport":{"type":"grpc"`
)

func TestVLESSGRPCTLSImportsServiceNames(t *testing.T) {
	for _, fixture := range []struct {
		entry, body, service string
	}{
		{"share link", vlessGRPCLink + "/?type=grpc&security=tls&mode=gun&serviceName=Svc.Mixed%2Fx%2B%E4%B8%80", "Svc.Mixed/x+一"},
		{"share link omitted", vlessGRPCLink + "?type=grpc&security=tls", ""},
		{"share link empty", vlessGRPCLink + "?type=grpc&security=tls&serviceName=", ""},
		{"Clash name", "proxies:\n" + vlessGRPCYAML + `, grpc-opts: {grpc-service-name: "Svc.Mixed/x+\u4e00"}}`, "Svc.Mixed/x+一"},
		{"Clash omitted", "proxies:\n" + vlessGRPCYAML + "}", "GunService"},
		{"Clash empty", "proxies:\n" + vlessGRPCYAML + `, grpc-opts: {grpc-service-name: ""}}`, "GunService"},
		{"native name", vlessGRPCJSON + `,"service_name":"Svc.Mixed/x+\u4e00"}}`, "Svc.Mixed/x+一"},
		{"native omitted", vlessGRPCJSON + "}}", ""},
		{"native empty", vlessGRPCJSON + `,"service_name":""}}`, ""},
	} {
		t.Run(fixture.entry, func(t *testing.T) {
			item := parseTestSubscription(t, fixture.body, 1)[0]
			out := testOutbound(t, item)
			transport, ok := out["transport"].(map[string]any)
			if !ok || transport["type"] != "grpc" || transport["service_name"] != fixture.service || item.Route.Security != "tls" {
				t.Fatalf("lost VLESS gRPC transport or service name: %v", transport)
			}
		})
	}
}

func TestVLESSGRPCTLSRejectsIncompatibleItems(t *testing.T) {
	const linkQuery = "?type=grpc&security=tls&serviceName=Valid"
	const link = vlessGRPCLink + linkQuery
	const clashNode = vlessGRPCYAML + ", grpc-opts: {grpc-service-name: Valid"
	const nativeNode = vlessGRPCJSON + `,"service_name":"Valid"`
	for _, fixture := range []struct {
		entry, body   string
		firstLine     int
		rejectedCodes []string
	}{
		{
			entry: "share links",
			body: strings.Join([]string{
				link + "&mode=multi",
				link + "&authority=other.example.test",
				link + "&host=other.example.test",
				link + "&path=%2Falt%2FTunMulti",
				vlessGRPCLink + "/alt/TunMulti" + linkQuery,
				vlessGRPCLink + "?type=grpc&security=tls&serviceName=%FF",
				link,
			}, "\n"),
			firstLine:     1,
			rejectedCodes: []string{"unsupported_field", "unsupported_field", "unsupported_field", "unsupported_field", "unsupported_field", "route_invalid"},
		},
		{
			entry: "Clash",
			body: strings.Join([]string{
				"proxies:",
				clashNode + ", grpc-authority: other.example.test}}",
				clashNode + ", mode: multi}}",
				clashNode + ", path: /other}}",
				vlessGRPCYAML + ", grpc-opts: {grpc-service-name: 123}}",
				clashNode + "}}",
			}, "\n"),
			firstLine:     2,
			rejectedCodes: []string{"unsupported_field", "unsupported_field", "unsupported_field", "route_invalid"},
		},
		{
			entry: "native JSON",
			body: strings.Join([]string{
				`{"outbounds":[`,
				nativeNode + `,"authority":"other.example.test"}},`,
				nativeNode + `,"mode":"multi"}},`,
				nativeNode + `,"path":"/other"}},`,
				vlessGRPCJSON + `,"service_name":null}},`,
				nativeNode + "}}]}",
			}, "\n"),
			firstLine:     2,
			rejectedCodes: []string{"unsupported_field", "unsupported_field", "unsupported_field", "route_invalid"},
		},
	} {
		t.Run(fixture.entry, func(t *testing.T) {
			items := parseTestSubscription(t, fixture.body, len(fixture.rejectedCodes)+1)
			for index, code := range fixture.rejectedCodes {
				item := items[index]
				line := fixture.firstLine + index
				if item.Route != nil || item.Error != code || item.Line != line {
					t.Fatalf("expected line %d rejected as %s, got line %d with code %q", line, code, item.Line, item.Error)
				}
			}
			valid := items[len(items)-1]
			if valid.Line != fixture.firstLine+len(fixture.rejectedCodes) {
				t.Fatal("lost successful node's source position")
			}
			testOutbound(t, valid)
		})
	}
}
