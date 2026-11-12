package importers

import (
	"encoding/base64"
	"testing"
)

func TestParseLinkPreservesWSRealityAndRejectsXHTTP(t *testing.T) {
	inputs := []struct{ raw, protocol, transport, security string }{
		{"vless://11111111-1111-4111-8111-111111111111@example.com:443?type=ws&security=tls&sni=example.com&host=cdn.example.com&path=%2Fws#Name", "vless", "ws", "tls"},
		{"vless://11111111-1111-4111-8111-111111111111@example.com:443?type=tcp&security=reality&sni=example.com&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=abcd&fp=chrome&flow=xtls-rprx-vision", "vless", "tcp", "reality"},
		{"hysteria2://secret@example.com:443?sni=example.com&insecure=1#HY", "hysteria2", "quic", "tls"},
	}
	for _, test := range inputs {
		route, err := ParseLink(test.raw)
		if err != nil {
			t.Fatal(err)
		}
		if route.ProtocolName != test.protocol || route.Transport != test.transport || route.Security != test.security {
			t.Fatal("lost connection fields")
		}
	}
	if _, err := ParseLink("vless://11111111-1111-4111-8111-111111111111@example.com:443?type=xhttp&security=tls"); err == nil {
		t.Fatal("XHTTP silently converted")
	}
}
func TestBatchPartialErrorsAndVMessBase64(t *testing.T) {
	link := "vmess://" + base64.RawStdEncoding.EncodeToString([]byte(`{"v":"2","ps":"VM","add":"example.com","port":"443","id":"11111111-1111-4111-8111-111111111111","aid":"0","net":"ws","path":"/ws","host":"example.com","tls":"tls","sni":"example.com"}`))
	body := base64.StdEncoding.EncodeToString([]byte(link + "\nunknown://bad\n"))
	batch, err := ParseSubscription([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Items) != 2 || batch.Items[0].Route == nil || batch.Items[1].Error == "" {
		t.Fatal("partial error not preserved")
	}
	if batch.Items[0].Route.WSPath != "/ws" {
		t.Fatal("WS path lost")
	}
}

func TestStructuredSubscriptionIsOneFormatError(t *testing.T) {
	for _, body := range []string{
		"proxies:\n  - name: Fixture\n    type: trojan\n    server: example.test\n    help: https://example.test/help",
		`{"outbounds":[{"type":"trojan","server":"example.test"}],"help":"https://example.test/help"}`,
		"<!DOCTYPE html>\n<html><a href=\"https://example.test\">login</a></html>",
	} {
		for _, input := range []string{body, base64.StdEncoding.EncodeToString([]byte(body))} {
			batch, err := ParseSubscription([]byte(input))
			if err == nil || err.Error() != "subscription_format_unsupported" || len(batch.Items) != 0 {
				t.Fatal("structured response masqueraded as protocol errors", len(batch.Items), err)
			}
		}
	}
}

func TestSubscriptionWSIgnoresInactiveGRPCMode(t *testing.T) {
	route, err := ParseLink("vless://11111111-1111-4111-8111-111111111111@example.test:443?type=ws&security=tls&mode=multi&host=edge.test&sni=identity.test&path=%2Fpath#Fixture")
	if err != nil || route.Transport != "ws" || route.WSHost != "edge.test" || route.ServerName != "identity.test" || route.WSPath != "/path" {
		t.Fatal(route.Transport, err)
	}
	for _, test := range []struct{ link, code string }{

		{"vless://11111111-1111-4111-8111-111111111111@example.test:443?type=xhttp&mode=packet-up", "unsupported_transport_xhttp"},
		{"vless://11111111-1111-4111-8111-111111111111@example.test:443?type=ws&security=tls&mode=unknown", "unsupported_field"},
	} {
		_, err := ParseLink(test.link)
		if err == nil || err.Error() != test.code {
			t.Fatal(test.code, err)
		}
	}
}

func TestAnyTLSImportPreservesTLSAndPassword(t *testing.T) {
	p, err := ParseLink("anytls://fixture@example.test:443?sni=identity.test&insecure=1#AnyTLS")
	if err != nil || p.ProtocolName != "anytls" || p.Password != "fixture" || p.ServerName != "identity.test" || p.Transport != "tcp" || !p.TLSSkipSuggested {
		t.Fatal("AnyTLS import lost fields", err)
	}
	if _, err := ParseLink("anytls://fixture@example.test:443?type=ws"); err == nil {
		t.Fatal("AnyTLS was converted to WS")
	}
}
