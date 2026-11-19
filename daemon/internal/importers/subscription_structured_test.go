package importers

import (
	"encoding/base64"
	"strings"
	"testing"

	"pax-connect/daemon/internal/config"
)

func parseTestSubscription(t *testing.T, body string, count int) []ParsedItem {
	t.Helper()
	batch, err := ParseSubscription([]byte(body))
	if err != nil || len(batch.Items) != count {
		t.Fatalf("expected %d rows, got %d (%v)", count, len(batch.Items), err)
	}
	return batch.Items
}

func testOutbound(t *testing.T, item ParsedItem) map[string]any {
	t.Helper()
	if item.Route == nil || item.Error != "" {
		t.Fatalf("line %d %s: %s", item.Line, item.Protocol, item.Error)
	}
	object, err := config.ParseDocument(item.Route.Document)
	if err != nil {
		t.Fatal(err)
	}
	return object
}

func TestClashInlineOutboundMapping(t *testing.T) {
	body := `
# position 2
mixed-port: 18888
proxy-groups:
  - {name: fixture, type: select, proxies: [plain]}
proxies:
  - {name: vision, type: vless, server: relay.example.test, port: 443, uuid: 11111111-1111-4111-8111-111111111111, tls: true, network: tcp, servername: identity.example.test, flow: xtls-rprx-vision, client-fingerprint: chrome, udp: false, reality-opts: {public-key: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA, short-id: abcd}}
  - {name: plain, type: anytls, server: relay.example.test, port: 1443, password: fixture, sni: another.example.test, udp: false, padding-scheme: "stop=8\n0=30-30"}
  - {name: quic, type: hysteria2, server: relay.example.test, port: 5443, password: fixture, skip-cert-verify: true, obfs: salamander, obfs-password: fixture-obfs, alpn: [h3]}
  - {name: encrypted, type: ss, server: relay.example.test, port: 8388, cipher: 2022-blake3-aes-128-gcm, password: MDEyMzQ1Njc4OWFiY2RlZg==, udp: true}
tun:
  enable: true
`
	items := parseTestSubscription(t, body, 4)
	vision := testOutbound(t, items[0])
	if items[0].Line != 7 || items[0].Route.Security != "reality" || items[0].Route.Flow != "xtls-rprx-vision" ||
		items[0].Route.RealityShortID != "abcd" || items[0].Route.Fingerprint != "chrome" || vision["network"] != "tcp" {
		t.Fatal("Clash VLESS lost transport or security settings")
	}
	plain := testOutbound(t, items[1])
	if items[1].Route.ServerName != "another.example.test" || plain["network"] != "tcp" || plain["padding-scheme"] != nil {
		t.Fatal("Clash AnyTLS lost TCP restriction or copied a server-only option")
	}
	testOutbound(t, items[2])
	if items[2].Route.Port != 5443 || items[2].Route.Transport != "quic" || items[2].Route.ObfsPassword != "fixture-obfs" ||
		items[2].Route.ServerName != "relay.example.test" || !items[2].Route.TLSSkipSuggested {
		t.Fatal("Clash Hysteria2 lost connection settings")
	}
	encrypted := testOutbound(t, items[3])
	if items[3].Route.ProtocolName != "shadowsocks" || items[3].Route.Method != "2022-blake3-aes-128-gcm" || encrypted["network"] != nil {
		t.Fatal("Clash Shadowsocks lost cipher or UDP permission")
	}
}

func TestClashWebsocketAndUpgradeKeepHTTPAndTLSIdentity(t *testing.T) {
	items := parseTestSubscription(t, `proxies:
  - {name: ws, type: vless, server: edge.example.test, port: 443, uuid: 11111111-1111-4111-8111-111111111111, tls: true, network: ws, ws-opts: {path: /ws, headers: {host: ws.example.test}}}
  - {name: upgrade, type: vless, server: edge.example.test, port: 443, uuid: 11111111-1111-4111-8111-111111111111, tls: true, servername: sni.example.test, network: ws, ws-opts: {path: /upgrade, v2ray-http-upgrade: true, headers: {Host: ws.example.test}}}
`, 2)
	ws := testOutbound(t, items[0])
	if items[0].Route.Transport != "ws" || items[0].Route.WSHost != "ws.example.test" || items[0].Route.ServerName != "ws.example.test" || ws["network"] != "tcp" {
		t.Fatal("Clash WS lost host-based TLS identity or disabled UDP default")
	}
	testOutbound(t, items[1])
	if items[1].Route.Transport != "httpupgrade" || items[1].Route.WSHost != "ws.example.test" || items[1].Route.ServerName != "sni.example.test" {
		t.Fatal("Clash WS upgrade lost host or SNI")
	}
}

func TestSingboxInlineOutboundsKeepNativeFields(t *testing.T) {
	const body = `
{
  "outbounds": [
    {"type":"selector","tag":"select","outbounds":["vision"]},
    {"type":"vless","tag":"vision","server":"relay.example.test","server_port":443,"uuid":"11111111-1111-4111-8111-111111111111",
      "packet_encoding":"xudp","network":["tcp","udp"],"flow":"xtls-rprx-vision",
      "tls":{"enabled":true,"server_name":"identity.example.test","reality":{"enabled":true,"public_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","short_id":"abcd"},"utls":{"enabled":true,"fingerprint":"chrome"}}},
    {"type":"anytls","tag":"plain","server":"relay.example.test","server_port":1443,"password":"fixture","tls":{"enabled":true,"alpn":"http/1.1"},"padding_scheme":["stop=8","0=30-30"],"network":["tcp"]},
    {"type":"hysteria2","tag":"quic","server":"relay.example.test","server_port":5443,"password":"fixture","tls":{"enabled":true,"insecure":true},"obfs":{"type":"salamander","password":"fixture-obfs"}},
    {"type":"shadowsocks","tag":"encrypted","server":"relay.example.test","server_port":8388,"method":"aes-128-gcm","password":"fixture"},
    {"type":"direct","tag":"direct","domain_resolver":{"server":"ignored"}}
  ],
  "inbounds":[{"type":"tun"}],
  "route":{"final":"select"}
}`
	items := parseTestSubscription(t, body, 4)
	vision := testOutbound(t, items[0])
	if items[0].Line != 5 || vision["packet_encoding"] != "xudp" || vision["network"] != nil || items[0].Route.Flow != "xtls-rprx-vision" {
		t.Fatal("native VLESS lost packet encoding or misplaced row")
	}
	plain := testOutbound(t, items[1])
	if items[1].Line != 8 || plain["padding_scheme"] != nil || plain["network"] != "tcp" ||
		items[1].Route.ServerName != "relay.example.test" || len(items[1].Route.ALPN) != 1 {
		t.Fatal("native AnyTLS lost transport semantics")
	}
	testOutbound(t, items[2])
	if items[2].Route.ObfsPassword != "fixture-obfs" || !items[2].Route.TLSSkipSuggested {
		t.Fatal("native Hysteria2 lost security settings")
	}
	testOutbound(t, items[3])
	if items[3].Route.Method != "aes-128-gcm" {
		t.Fatal("native Shadowsocks lost cipher")
	}
}

func TestMixedStructuredBodyKeepsGoodRowsAndPositions(t *testing.T) {
	tests := []struct {
		name, source, rejectedCode string
		first, second              int
	}{
		{"conflicting YAML keys", `---
proxies:
  - {name: First, type: anytls, server: relay.example.test, port: 443, password: fixture}
  - {name: Rejected, type: anytls, server: relay.example.test, port: 443, port: 444, password: fixture}
`, "route_invalid", 3, 4},
		{"YAML alias", `spare: &fixture {name: alias}
proxies:
  - {name: First, type: anytls, server: relay.example.test, port: 443, password: fixture}
  - {name: Rejected, type: anytls, server: relay.example.test, port: 443, password: *fixture}
`, "route_invalid", 3, 4},
		{"Clash XHTTP", `proxies:
  - {name: First, type: anytls, server: relay.example.test, port: 443, password: fixture}
  - {name: Rejected, type: vless, network: xhttp, xhttp-opts: {path: /}}
`, "unsupported_transport_xhttp", 2, 3},
		{"JSON duplicate security key", `{"outbounds":[
  {"type":"anytls","tag":"First","server":"relay.example.test","server_port":443,"password":"fixture","tls":{"enabled":true}},
  {"type":"anytls","tag":"Rejected","server":"relay.example.test","server_port":443,"password":"fixture","tls":{"enabled":true,"enabled":false}}
]}`, "route_invalid", 2, 3},
		{"JSON XHTTP", `{"outbounds":[
  {"type":"anytls","tag":"First","server":"relay.example.test","server_port":443,"password":"fixture","tls":{"enabled":true}},
  {"type":"vless","tag":"Rejected","transport":{"type":"xhttp","mode":"auto"}}
]}`, "unsupported_transport_xhttp", 2, 3},
		{"JSON Kubernetes-like options", `{"outbounds":[
  {"type":"anytls","tag":"First","server":"relay.example.test","server_port":443,"password":"fixture","tls":{"enabled":true}},
  {"type":"anytls","tag":"Rejected","transport":{"type":"tcp"},"domain_resolver":"private-fixture"}
]}`, "unsupported_field", 2, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := parseTestSubscription(t, tt.source, 2)
			testOutbound(t, items[0])
			if items[0].Line != tt.first || items[1].Line != tt.second || items[1].Route != nil || items[1].Error != tt.rejectedCode {
				t.Fatalf("unexpected partial result at lines %d, %d: %q", items[0].Line, items[1].Line, items[1].Error)
			}
		})
	}
}

func TestClashRemoteProvidersLeaveMixedImportIncomplete(t *testing.T) {
	const provider = "proxy-providers:\n  fixtures:\n    type: http\n    url: https://example.test/subscription\n"
	batch, err := ParseSubscription([]byte(provider))
	if err != errRemoteProvider || len(batch.Items) != 0 {
		t.Fatal("missing provider-only error", err)
	}
	items := parseTestSubscription(t, provider+"proxies:\n  - {name: plain, type: anytls, server: relay.example.test, port: 443, password: fixture}\n", 2)
	testOutbound(t, items[0])
	if items[1].Error != errRemoteProvider.Error() || items[1].Line != 1 || items[1].Name != "" || items[1].Route != nil {
		t.Fatal("invalid mixed-provider report")
	}
}

func TestBase64AnyTLSUDPAndSingleJSON(t *testing.T) {
	links := "\nanytls://fixture@relay.example.test:443?sni=identity.example.test&insecure=0&udp=0#One\nanytls://fixture@relay.example.test:443?udp=1#Two\n"
	items := parseTestSubscription(t, base64.StdEncoding.EncodeToString([]byte(links)), 2)
	if testOutbound(t, items[0])["network"] != "tcp" || items[0].Line != 2 || items[0].Route.TLSSkipSuggested {
		t.Fatal("Base64 AnyTLS lost TCP-only or strict TLS")
	}
	if items[1].Route == nil || items[1].Route.Document != nil {
		t.Fatal("AnyTLS with UDP allowed diverged from the original link representation")
	}
	const json = "\n  " + `{"type":"shadowsocks","tag":"fixture","server":"relay.example.test","server_port":8388,"method":"aes-128-gcm","password":"fixture","network":"tcp"}`
	item := parseTestSubscription(t, json, 1)[0]
	if item.Line != 2 || testOutbound(t, item)["network"] != "tcp" {
		t.Fatal("Single JSON outbound lost line position or TCP-only setting")
	}
}

func TestStructuredSubscriptionShapeAndLimits(t *testing.T) {
	for _, body := range []string{
		`{"outbounds":[],"outbounds":[]}`,
		`{"outbounds":{}}`,
		"proxies: []\nproxies: []",
		"proxies: []\n---\nproxies: []",
	} {
		batch, err := ParseSubscription([]byte(body))
		if err != errImportFormat || len(batch.Items) != 0 {
			t.Fatal("ambiguous root yielded rows", err)
		}
	}
	for _, body := range []string{
		"proxies:\n" + strings.Repeat("  - {}\n", maxSubscriptionItems+1),
		`{"outbounds":[` + strings.Repeat(`{"type":"direct"},`, maxSubscriptionItems) + "{}]}",
	} {
		batch, err := ParseSubscription([]byte(body))
		if err != errImportTooLarge || len(batch.Items) != 0 {
			t.Fatal("node limit did not apply to all entries", err)
		}
	}
	items := parseTestSubscription(t, "proxies:\n- {name: over, type: anytls, server: relay.example.test, port: 443, password: fixture, padding-scheme: "+strings.Repeat("x", config.MaxDocumentBytes)+"}", 1)
	if items[0].Error != errImportTooLarge.Error() {
		t.Fatal("omitted provider field did not count against item bounds")
	}
}
