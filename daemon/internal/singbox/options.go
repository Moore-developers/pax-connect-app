package singbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/sagernet/sing-box/option"
	"pax-connect/daemon/internal/config"
)

// ErrConfig hides upstream parser messages that may expose private configuration.
var ErrConfig = errors.New("kernel config invalid")

// ErrRules indicates missing or altered bundled rules.
var ErrRules = errors.New("bundled rules invalid")

// Rules identifies the two pinned local assets, never arbitrary RPC paths.
const remoteDNSServer = "1.1.1.1"

type Rules struct {
	Directory   string
	GeositeHash string
	GoogleHash  string
	GeoIPHash   string
}

func validateRules(rules Rules) error {
	for name, expected := range map[string]string{"geosite-cn.srs": rules.GeositeHash, "geoip-cn.srs": rules.GeoIPHash, "geosite-google.srs": rules.GoogleHash} {
		data, err := os.ReadFile(filepath.Join(rules.Directory, name))
		if err != nil {
			return ErrRules
		}
		hash := sha256.Sum256(data)
		if expected == "" || hex.EncodeToString(hash[:]) != expected {
			return ErrRules
		}
	}
	return nil
}
func buildOptions(ctx context.Context, p config.DebugProfile, rules Rules, tun, ipv6 bool) (option.Options, error) {
	if err := config.ValidateAddressFamily(p); err != nil {
		return option.Options{}, err
	}
	if p.TLSResolved.NeedsConfirmation {
		return option.Options{}, config.ErrTLSConfirmation
	}
	if p.TLSResolved.Mode != config.TLSStrict && p.TLSResolved.Mode != config.TLSSkip && p.TLSResolved.Mode != "" {
		return option.Options{}, ErrConfig
	}
	planned, extraDNS, err := plannedOutbounds(p, "proxy")
	if err != nil {
		return option.Options{}, err
	}

	resolver, err := dnsPolicy(p, tun)
	if err != nil {
		return option.Options{}, err
	}
	servers := resolver["servers"].([]any)
	for _, server := range extraDNS {
		servers = append(servers, server)
	}
	resolver["servers"] = servers
	outbounds := []any{map[string]any{"type": "direct", "tag": "direct"}}
	for _, out := range planned {
		outbounds = append(outbounds, out)
	}
	doc := map[string]any{"log": map[string]any{"disabled": true}, "dns": resolver,
		"outbounds": outbounds,
		"route":     map[string]any{"final": config.DefaultRoutingOutbound, "auto_detect_interface": true, "find_process": tun}}
	if tun {
		if err := validateRules(rules); err != nil {
			return option.Options{}, err
		}
		doc["inbounds"] = []any{map[string]any{"type": "tun", "tag": "tun-in", "address": tunAddress(ipv6), "mtu": 1500, "auto_route": true, "dns_mode": "hijack", "stack": "system"}, defaultMixedInbound()}
		route := doc["route"].(map[string]any)
		route["rule_set"] = []any{
			map[string]any{"type": "local", "tag": "geosite-google", "format": "binary", "path": filepath.Join(rules.Directory, "geosite-google.srs")},
			map[string]any{"type": "local", "tag": "geosite-cn", "format": "binary", "path": filepath.Join(rules.Directory, "geosite-cn.srs")},
			map[string]any{"type": "local", "tag": "geoip-cn", "format": "binary", "path": filepath.Join(rules.Directory, "geoip-cn.srs")}}
		route["rules"] = config.RoutingPolicy(ipv6)
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return option.Options{}, ErrConfig
	}
	var result option.Options
	if err := result.UnmarshalJSONContext(ctx, data); err != nil {
		return option.Options{}, ErrConfig
	}
	return result, nil
}

// tunAddress mirrors the frozen address-family policy. On a network without a
// usable IPv6 path the TUN must not look IPv6-capable: sing-tun builds IPv6
// auto routes only when the interface carries an IPv6 address, so omitting the
// prefix makes connect fail immediately instead of the TUN completing the
// handshake and resetting afterwards (applications such as WeChat retry that
// forever instead of falling back to IPv4).
func tunAddress(ipv6 bool) []string {
	if !ipv6 {
		return []string{"172.29.231.1/30"}
	}
	return []string{"172.29.231.1/30", "fdfe:dcba:9876::1/126"}
}

func routeOutbound(p config.RouteConfig, tag string) (map[string]any, error) {
	if len(p.Document) > 0 {
		return documentOutbound(p, tag)
	}
	if p.ProtocolName == "hysteria2" && p.Security == "" {
		p.Security = "tls"
		p.Transport = "quic"
	}
	result := map[string]any{"type": p.ProtocolName, "tag": tag, "server": p.Server, "server_port": p.Port, "domain_resolver": map[string]any{"server": "dns-local"}}
	switch p.ProtocolName {
	case "socks", "http":
		if p.ProtocolName == "socks" {
			result["version"] = "5"
		}
		if p.Username != "" {
			result["username"] = p.Username
			result["password"] = p.Password
		}
	case "trojan", "anytls":
		result["password"] = p.Password
	case "shadowsocks":
		result["method"] = p.Method
		result["password"] = p.Password
	case "hysteria2":
		result["password"] = p.Password
		if p.ObfsPassword != "" {
			result["obfs"] = map[string]any{"type": "salamander", "password": p.ObfsPassword}
		}
	case "vmess":
		result["uuid"] = p.UUID
		result["alter_id"] = p.AlterID
		security := p.VMessSecurity
		if security == "" {
			security = "auto"
		}
		result["security"] = security
	case "vless":
		result["uuid"] = p.UUID
		if p.Flow != "" {
			result["flow"] = p.Flow
		}
	default:
		return nil, ErrConfig
	}
	if p.Security == "tls" || p.Security == "reality" {
		tls := map[string]any{"enabled": true, "server_name": p.ServerName, "insecure": p.Security == "tls" && p.TLSResolved.Mode == config.TLSSkip}
		if len(p.ALPN) > 0 {
			tls["alpn"] = p.ALPN
		}
		if p.Fingerprint != "" {
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": p.Fingerprint}
		}
		if p.Security == "reality" {
			tls["reality"] = map[string]any{"enabled": true, "public_key": p.RealityPublicKey, "short_id": p.RealityShortID}
		}
		result["tls"] = tls
	}
	if p.Transport == "httpupgrade" {
		result["transport"] = map[string]any{"type": "httpupgrade", "path": p.WSPath, "host": p.WSHost}
	}
	if p.Transport == "ws" {
		result["transport"] = map[string]any{"type": "ws", "path": p.WSPath, "headers": map[string]string{"Host": p.WSHost}}
	}
	return result, nil
}
