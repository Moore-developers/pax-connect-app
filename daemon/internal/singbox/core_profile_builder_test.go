//go:build with_quic

package singbox

import (
	"context"
	stdjson "encoding/json"
	"fmt"
	"testing"

	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing/common/json"
	"pax-connect/daemon/internal/config"
)

// Exercise real builder output, not hand-written approximations of its schema.
func TestCoreProfileBuilderMatrix(t *testing.T) {
	profiles := map[string]config.DebugProfile{}
	for name, protocol := range map[string]string{
		"hy2": "hysteria2", "hy2_obfs": "hysteria2", "trojan_ws_utls_alpn": "trojan",
		"vless_reality_flow": "vless", "vmess_httpupgrade": "vmess", "shadowsocks": "shadowsocks", "anytls": "anytls",
	} {
		p := testProfile()
		p.ProtocolName = protocol
		p.Security, p.Transport = "tls", "tcp"
		switch name {
		case "hy2", "hy2_obfs":
			p.Transport = "quic"
			if name == "hy2_obfs" {
				p.ObfsPassword = "fixture"
			}
		case "trojan_ws_utls_alpn":
			p.Transport, p.WSPath, p.WSHost = "ws", "/ws", "test.invalid"
			p.Fingerprint, p.ALPN = "chrome", []string{"h2", "http/1.1"}
		case "vless_reality_flow":
			p.Security, p.Flow = "reality", "xtls-rprx-vision"
			p.UUID = "00000000-0000-4000-8000-000000000001"
			p.RealityPublicKey, p.RealityShortID = "fixture", "01"
		case "vmess_httpupgrade":
			p.UUID = "00000000-0000-4000-8000-000000000001"
			p.Transport, p.WSPath, p.WSHost = "httpupgrade", "/upgrade", "test.invalid"
		case "shadowsocks":
			p.Security, p.Method = "none", "aes-128-gcm"
		}
		profiles[name] = p
	}
	ctx := include.Context(context.Background())
	rules, state := testRules(t), t.TempDir()
	for name, profile := range profiles {
		for _, tun := range []bool{false, true} {
			for _, ipv6 := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/tun=%t/ipv6=%t", name, tun, ipv6), func(t *testing.T) {
					opts, err := buildOptions(ctx, profile, rules, tun, ipv6)
					if err != nil {
						t.Fatal(err)
					}
					raw, err := json.MarshalContext(ctx, opts)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = ValidateCoreOptions(ctx, raw, state, 17893); err != nil {
						t.Fatalf("builder output rejected: %v", err)
					}
				})
			}
		}
	}
}

func TestCoreProfilePhysicalEgressBuilder(t *testing.T) {
	// Measurement assessment and CF candidate testing add bind_interface to a
	// route document before the same documentOutbound/buildOptions path.
	p := testProfile()
	p.ProtocolName, p.Security, p.Transport = "trojan", "tls", "ws"
	p.Document = []byte(`{"type":"trojan","server":"test.invalid","server_port":443,"password":"fixture","bind_interface":"en0","tls":{"enabled":true},"transport":{"type":"ws","path":"/ws"}}`)
	ctx := include.Context(context.Background())
	opts, err := buildOptions(ctx, p, testRules(t), false, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalContext(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := ValidateCoreOptions(ctx, raw, t.TempDir(), 17893)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Outbounds []struct {
			Tag           string `json:"tag"`
			BindInterface string `json:"bind_interface"`
		} `json:"outbounds"`
		Route struct {
			AutoDetectInterface bool `json:"auto_detect_interface"`
		} `json:"route"`
	}
	if err = stdjson.Unmarshal([]byte(canonical), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Outbounds) != 2 || doc.Outbounds[1].Tag != "proxy" || doc.Outbounds[1].BindInterface != "en0" || !doc.Route.AutoDetectInterface {
		t.Fatal("physical-egress builder fields lost")
	}
}
