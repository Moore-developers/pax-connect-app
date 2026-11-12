//go:build with_quic

package singbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	stdjson "encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing/common/json"
	"pax-connect/daemon/internal/config"
)

func testRules(t *testing.T) Rules {
	t.Helper()
	directory := filepath.Join("..", "..", "..", "rulesets")
	hash := func(name string) string {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		value := sha256.Sum256(data)
		return hex.EncodeToString(value[:])
	}
	return Rules{Directory: directory, GoogleHash: hash("geosite-google.srs"), GeositeHash: hash("geosite-cn.srs"), GeoIPHash: hash("geoip-cn.srs")}
}
func testProfile() config.DebugProfile {
	return config.DebugProfile{NodeProfile: config.NodeProfile{ID: "test", Name: "fixture", ProtocolName: "hysteria2", Server: "test.invalid", Port: 443}, SchemaVersion: 1, Password: "test-secret", ServerName: "test.invalid"}
}
func TestOptionsIPv4OnlyTUNCarriesNoIPv6(t *testing.T) {
	ctx := include.Context(context.Background())
	options, err := buildOptions(ctx, testProfile(), testRules(t), true, false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalContext(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]stdjson.RawMessage
	if err := stdjson.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	var route struct {
		Rules []struct {
			IPVersion int    `json:"ip_version"`
			Network   string `json:"network"`
			Action    string `json:"action"`
		}
	}
	if err := stdjson.Unmarshal(document["route"], &route); err != nil {
		t.Fatal(err)
	}
	blocked := false
	for _, rule := range route.Rules {
		if rule.Network == "udp" && rule.Action == "reject" {
			t.Fatal("hysteria2 UDP incorrectly blocked")
		}
		if rule.IPVersion == 6 && rule.Action == "reject" {
			blocked = true
		}
	}
	if !blocked {
		t.Fatal("IPv6 fallback missing")
	}
	var inbound []struct {
		Address stdjson.RawMessage `json:"address"`
		MTU     int                `json:"mtu"`
	}
	if err := stdjson.Unmarshal(document["inbounds"], &inbound); err != nil {
		t.Fatal(err)
	}
	if len(inbound) != 2 || inbound[0].MTU != 1500 {
		t.Fatalf("IPv4-only TUN contract: %s", document["inbounds"])
	}
	if addresses := tunAddressValues(t, inbound[0].Address); len(addresses) != 1 || addresses[0] != "172.29.231.1/30" {
		t.Fatalf("IPv4-only TUN must not advertise IPv6: %s", inbound[0].Address)
	}
}

// tunAddressValues accepts both Listable shapes: one prefix is a bare string,
// several are an array.
func tunAddressValues(t *testing.T, raw stdjson.RawMessage) []string {
	t.Helper()
	var many []string
	if err := stdjson.Unmarshal(raw, &many); err == nil {
		return many
	}
	var one string
	if err := stdjson.Unmarshal(raw, &one); err != nil {
		t.Fatal(err)
	}
	return []string{one}
}

func TestOptionsDualStackTUNKeepsIPv6Capture(t *testing.T) {
	p := testProfile()
	p.AddressFamilyState = "available"
	ctx := include.Context(context.Background())
	options, err := buildOptions(ctx, p, testRules(t), true, true)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalContext(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]stdjson.RawMessage
	if err := stdjson.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	var inbound []struct {
		Address stdjson.RawMessage `json:"address"`
	}
	if err := stdjson.Unmarshal(document["inbounds"], &inbound); err != nil {
		t.Fatal(err)
	}
	addresses := tunAddressValues(t, inbound[0].Address)
	if len(inbound) != 2 || len(addresses) != 2 || addresses[1] != "fdfe:dcba:9876::1/126" {
		t.Fatalf("dual-stack TUN must capture IPv6: %s", document["inbounds"])
	}
}
func TestCoreBuildRejectsAlteredAssets(t *testing.T) {
	rules := testRules(t)
	rules.GeositeHash = "bad"
	if _, err := CoreProfile(context.Background(), testProfile(), rules, "127.0.0.1:17890"); err != ErrRules {
		t.Fatalf("bad assets error=%v", err)
	}
}
