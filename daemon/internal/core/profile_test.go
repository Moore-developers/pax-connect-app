package core

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "pax-connect/daemon/gen/proto/corev1"
	"pax-connect/daemon/internal/singbox"
)

func TestProfilePoCsRejectBeforeRuntime(t *testing.T) {
	tests := []struct{ name, raw string }{
		{"01_tor_case", `{"outbounds":[{"type":"tor","tag":"t","Executable_Path":"/tmp/evil"}]}`},
		{"02_tor_exec", `{"outbounds":[{"type":"tor","tag":"t","executable_path":"/tmp/evil"}]}`},
		{"03_controller_case", `{"experimental":{"clash_api":{"External_Controller":"0.0.0.0:9090"}}}`},
		{"04_controller_loopback", `{"experimental":{"clash_api":{"external_controller":"127.0.0.1:9090"}}}`},
		{"05_log_case", `{"log":{"Output":"/etc/pax-poc.log"}}`},
		{"06_route_case", `{"Route":{"rule_set":[{"type":"remote","tag":"r","url":"https://x.invalid/r.srs"}]}}`},
		{"07_tls_case", `{"outbounds":[{"type":"trojan","tag":"x","server":"127.0.0.1","server_port":443,"password":"p","tls":{"enabled":true,"Certificate_Path":"/etc/master.passwd"}}]}`},
		{"08_debug", `{"experimental":{"debug":{"listen":"0.0.0.0:6060"}}}`},
		{"09_tor_data", `{"outbounds":[{"type":"tor","tag":"t","data_directory":"/etc/evil"}]}`},
		{"10_hysteria_masquerade", `{"inbounds":[{"type":"hysteria2","listen":"0.0.0.0","listen_port":8443,"users":[{"password":"x"}],"masquerade":"file:///","tls":{"enabled":true,"certificate":["x"],"key":["x"]}}]}`},
		{"11_cert_provider", `{"certificate_providers":[{"type":"cloudflare-origin-ca","tag":"c","data_directory":"/etc/evil"}]}`},
		{"12_mixed_public", `{"inbounds":[{"type":"mixed","tag":"m","listen":"0.0.0.0","listen_port":7890}]}`},
		{"13_duplicate", `{"log":{},"log":{"output":"/x"}}`},
		{"14_unicode", `{"log":{"\u006futput":"/etc/pax-poc.log"}}`},
		{"system_proxy_wrong_port", `{"inbounds":[{"type":"mixed","listen":"127.0.0.1","listen_port":8,"set_system_proxy":true}]}`},
		{"system_proxy_two", `{"inbounds":[{"type":"mixed","listen":"127.0.0.1","listen_port":17893,"set_system_proxy":true},{"type":"mixed","listen":"127.0.0.1","listen_port":17893,"set_system_proxy":true}]}`},
		{"system_proxy_non_mixed", `{"inbounds":[{"type":"http","listen":"127.0.0.1","listen_port":17893,"set_system_proxy":true}]}`},
		{"system_proxy_tun", `{"inbounds":[{"type":"tun","set_system_proxy":true}]}`},
		{"system_proxy_ipv6", `{"inbounds":[{"type":"mixed","listen":"::1","listen_port":17893,"set_system_proxy":true}]}`},
		{"tls_spoof_poc", `{"outbounds":[{"type":"trojan","tag":"t","server":"x","server_port":1,"password":"p","tls":{"enabled":true,"spoof":"x.com","spoof_method":"wrong-sequence","kernel_tx":true}}]}`},
		{"default_interface", `{"route":{"default_interface":"en0"}}`},
		{"default_mark", `{"route":{"default_mark":1}}`},
		{"routing_mark", `{"outbounds":[{"type":"direct","routing_mark":1}]}`},
		{"direct_action_bind_interface", `{"route":{"rules":[{"action":"direct","bind_interface":"en0"}]}}`},
		{"duplicate_safe_last", `{"log":{"output":"/x"},"log":{}}`},
		{"duplicate_nested", `{"log":{"level":"info","level":"error"}}`},
		{"unknown_zero", `{"log":{"unrecognized":false}}`},
		{"unknown_custom", `{"outbounds":[{"type":"direct","domain_resolver":{"server":"d","unrecognized":false}}]}`},
		{"known_forbidden_zero", `{"outbounds":[{"type":"direct","protect_path":""}]}`},
		{"case_zero", `{"log":{"Disabled":false}}`},
		{"tor", `{"outbounds":[{"type":"tor"}]}`},
		{"ssh", `{"outbounds":[{"type":"ssh","server":"x","server_port":22}]}`},
		{"wireguard", `{"endpoints":[{"type":"wireguard","tag":"w"}]}`},
		{"service", `{"services":[{"type":"resolved"}]}`},
		{"ntp", `{"ntp":{"enabled":true}}`},
		{"v2ray", `{"experimental":{"v2ray_api":{"listen":"127.0.0.1:9091"}}}`},
		{"dhcp_files", `{"route":{"dhcp_lease_files":["/etc/x"]}}`},
		{"hosts", `{"dns":{"servers":[{"type":"hosts","path":["/etc/hosts"]}]}}`},
		{"dns_other", `{"dns":{"servers":[{"type":"local"}]}}`},
		{"cache_foreign", `{"experimental":{"cache_file":{"path":"/tmp/cache.db"}}}`},
		{"remote_rule", `{"route":{"rule_set":[{"type":"remote","tag":"geosite-cn","format":"binary","url":"https://x.invalid/r.srs"}]}}`},
		{"source_rule", `{"route":{"rule_set":[{"type":"local","tag":"geosite-cn","format":"source","path":"/tmp/x"}]}}`},
		{"unknown_rule_tag", `{"route":{"rule_set":[{"type":"local","tag":"other","format":"binary"}]}}`},
		{"mixed_missing", `{"inbounds":[{"type":"mixed"}]}`},
		{"mixed_ipv6_public", `{"inbounds":[{"type":"mixed","listen":"::"}]}`},
		{"namespace", `{"outbounds":[{"type":"direct","netns":"/var/run/netns/x"}]}`},
		{"protect_socket", `{"outbounds":[{"type":"direct","protect_path":"/tmp/sock"}]}`},
		{"plugin", `{"outbounds":[{"type":"shadowsocks","server":"x","server_port":443,"method":"aes-128-gcm","password":"fake","plugin":"/tmp/exec"}]}`},
	}
	for field, value := range map[string]string{"spoof": `"x.com"`, "spoof_method": `"wrong-sequence"`, "kernel_tx": `true`, "kernel_rx": `true`} {
		tests = append(tests, struct{ name, raw string }{"tls_" + field, `{"outbounds":[{"type":"trojan","server":"x","server_port":1,"password":"p","tls":{"enabled":true,"` + field + `":` + value + `}}]}`})
	}
	for _, action := range []string{"route", "route-options"} {
		for field, value := range map[string]string{"tls_spoof": `"x.com"`, "tls_spoof_method": `"wrong-sequence"`} {
			tests = append(tests, struct{ name, raw string }{action + "_" + field, `{"route":{"rules":[{"action":"` + action + `","` + field + `":` + value + `}]}}`})
		}
	}
	for _, field := range []string{"certificate_path", "client_certificate_path", "client_key_path", "key_path", "acme"} {
		value := `"/tmp/private"`
		if field == "acme" {
			value = `{"domain":["x"],"data_directory":"/etc/x"}`
		}
		tests = append(tests, struct{ name, raw string }{"tls_" + field, `{"outbounds":[{"type":"trojan","server":"x","server_port":443,"password":"fake","tls":{"enabled":true,"` + field + `":` + value + `}}]}`})
	}
	for _, field := range []string{"config_path", "key_path"} {
		tests = append(tests, struct{ name, raw string }{"ech_" + field, `{"outbounds":[{"type":"trojan","server":"x","server_port":443,"password":"fake","tls":{"enabled":true,"ech":{"enabled":true,"` + field + `":"/tmp/private"}}}]}`})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &fakeRuntime{state: "STARTED"}
			s := testServer(t, r)
			_, err := s.ApplyProfile(context.Background(), &pb.ApplyProfileRequest{ProfileJson: tt.raw})
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("validation code %s", status.Code(err))
			}
			t.Logf("rejected before runtime: %s", status.Convert(err).Message())
			if len(r.calls) != 0 || r.profile != "" || r.state != "STARTED" {
				t.Fatal("rejected input reached runtime")
			}
		})
	}
}

func TestProfileAcceptAndCanonicalRuntime(t *testing.T) {
	nodes := map[string]string{
		"direct": `{"type":"direct"}`, "block": `{"type":"block"}`,
		"selector": `{"type":"selector","outbounds":["direct"]}`, "urltest": `{"type":"urltest","outbounds":["direct"]}`,
		"vmess":       `{"type":"vmess","server":"example.invalid","server_port":443,"uuid":"00000000-0000-4000-8000-000000000001","security":"auto","tls":{"enabled":true},"transport":{"type":"ws","path":"/ws","headers":{"Host":"example.invalid"}}}`,
		"vless":       `{"type":"vless","server":"example.invalid","server_port":443,"uuid":"00000000-0000-4000-8000-000000000001","tls":{"enabled":true,"reality":{"enabled":true,"public_key":"fake","short_id":"01"}}}`,
		"trojan":      `{"type":"trojan","server":"example.invalid","server_port":443,"password":"fake","tls":{"enabled":true,"certificate":["fake PEM"],"client_certificate":["fake PEM"],"client_key":["fake PEM"],"fragment":true,"ech":{"enabled":true,"config":["fake"]}}}`,
		"shadowsocks": `{"type":"shadowsocks","server":"example.invalid","server_port":443,"method":"aes-128-gcm","password":"fake"}`,
		"hysteria2":   `{"type":"hysteria2","server":"example.invalid","server_port":443,"password":"fake","obfs":{"type":"salamander","password":"fake"},"tls":{"enabled":true}}`,
		"anytls":      `{"type":"anytls","server":"example.invalid","server_port":443,"password":"fake","tls":{"enabled":true}}`,
		"socks":       `{"type":"socks","server":"example.invalid","server_port":1080,"username":"fake","password":"fake"}`,
		"http":        `{"type":"http","server":"example.invalid","server_port":443,"username":"fake","password":"fake","path":"/proxy","tls":{"enabled":true}}`,
	}
	fixtures := map[string]string{
		"process_path":           `{"route":{"rules":[{"process_path":["/Applications/Fake.app/Contents/MacOS/Fake"],"process_name":["Fake"],"outbound":"direct"}]},"dns":{"servers":[{"type":"udp","tag":"local","server":"127.0.0.1"},{"type":"https","tag":"remote","server":"example.invalid","path":"/dns-query"}],"rules":[{"process_path":["/usr/bin/fake"],"server":"local"}]}}`,
		"probe_TUN_mixed_direct": `{"inbounds":[{"type":"tun","tag":"tun-in","address":["172.19.77.1/30"],"mtu":1500,"auto_route":true,"stack":"system"},{"type":"mixed","tag":"mixed-in","listen":"127.0.0.1","listen_port":17893,"set_system_proxy":true}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct","auto_detect_interface":true}}`,
		"mixed_ipv6":             `{"inbounds":[{"type":"mixed","listen":"::1","listen_port":7890}]}`,
		"empty":                  `{}`,
		"clash_cleared":          `{"experimental":{"debug":{},"v2ray_api":{},"clash_api":{"secret":"fake","external_controller":"","external_ui":"","external_ui_download_url":"","default_mode":"global","store_selected":true}}}`,
	}
	for name, node := range nodes {
		fixtures[name] = `{"outbounds":[` + node + `]}`
	}
	for name, raw := range fixtures {
		t.Run(name, func(t *testing.T) {
			r := &fakeRuntime{}
			s := testServer(t, r)
			// Every accepted fixture also carries tag-only, binary rules; caller paths
			// deliberately point outside resources and must be ignored, never opened.
			var doc map[string]any
			if err := json.Unmarshal([]byte(raw), &doc); err != nil {
				t.Fatal(err)
			}
			route, _ := doc["route"].(map[string]any)
			if route == nil {
				route = map[string]any{}
				doc["route"] = route
			}
			route["rule_set"] = []any{map[string]any{"type": "local", "tag": "geosite-cn", "format": "binary", "path": "/tmp/user-controlled"}, map[string]any{"type": "local", "tag": "geoip-cn", "format": "binary"}}
			input, _ := json.MarshalIndent(doc, "", "  ")
			expected, err := singbox.ValidateCoreOptions(context.Background(), input, s.opts.State, s.opts.MixedPort)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.ApplyProfile(context.Background(), &pb.ApplyProfileRequest{ProfileJson: string(input), Revision: 1})
			if err != nil {
				t.Fatal(err)
			}
			if r.profile == string(input) || r.profile != expected || len(r.calls) != 1 {
				t.Fatal("runtime did not receive canonical bytes")
			}
			if err = json.Unmarshal([]byte(r.profile), &doc); err != nil {
				t.Fatal(err)
			}
			exp := doc["experimental"].(map[string]any)
			if exp["cache_file"].(map[string]any)["path"] != filepath.Join(s.opts.State, "cache.db") || exp["debug"] != nil || exp["clash_api"] != nil || exp["v2ray_api"] != nil {
				t.Fatal("forced experiment fields missing")
			}
			for _, rule := range doc["route"].(map[string]any)["rule_set"].([]any) {
				v := rule.(map[string]any)
				if v["path"] != filepath.Join(s.opts.State, "rulesets", v["tag"].(string)+".srs") {
					t.Fatal("ruleset copy path missing")
				}
			}
			if strings.Contains(r.profile, "user-controlled") {
				t.Fatal("raw path survived")
			}
		})
	}
}
func TestProfileSizeAndSyntax(t *testing.T) {
	for _, tt := range []struct{ name, raw, reason string }{
		{"exact", `{}` + strings.Repeat(" ", maxProfileBytes-2), ""},
		{"overflow", `{}` + strings.Repeat(" ", maxProfileBytes-1), "core_profile_too_large"},
		{"size_before_syntax", strings.Repeat("!", maxProfileBytes+1), "core_profile_too_large"},
		{"depth", strings.Repeat("[", 1024) + strings.Repeat("]", 1024), "core_profile_invalid"},
		{"malformed", `{`, "core_profile_invalid"}, {"null", `null`, "core_profile_invalid"}, {"array", `[]`, "core_profile_invalid"}, {"trailing", `{} {}`, "core_profile_invalid"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := singbox.ValidateCoreOptions(context.Background(), []byte(tt.raw), t.TempDir(), 17893)
			if tt.reason == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != tt.reason {
				t.Fatal(err)
			}
		})
	}
}

func TestCoreLogClampAndCache(t *testing.T) {
	state := t.TempDir()
	for _, tt := range []struct{ level, want string }{{"trace", "info"}, {"debug", "info"}, {"info", "info"}, {"warn", "warn"}, {"error", "error"}, {"fatal", "error"}, {"panic", "error"}, {"invented", "info"}} {
		t.Run(tt.level, func(t *testing.T) {
			raw := `{"log":{"disabled":true,"level":"` + tt.level + `","output":""},"endpoints":[],"services":[],"certificate_providers":[],"experimental":{"cache_file":{"enabled":false,"path":` + strconvJSON(filepath.Join(state, "cache.db")) + ` ,"store_fakeip":true,"store_rdrc":true,"store_dns":true}}}`
			result, err := singbox.ValidateCoreOptions(context.Background(), []byte(raw), state, 17893)
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err = json.Unmarshal([]byte(result), &doc); err != nil {
				t.Fatal(err)
			}
			log := doc["log"].(map[string]any)
			if log["level"] != tt.want || log["output"] != "/dev/null" || log["disabled"] == true {
				t.Fatal("unclamped log options")
			}
			cache := doc["experimental"].(map[string]any)["cache_file"].(map[string]any)
			if cache["enabled"] != true || cache["store_fakeip"] != nil || cache["store_rdrc"] != nil || cache["store_dns"] != nil {
				t.Fatal("cache not forced")
			}
		})
	}
}
func strconvJSON(s string) string { b, _ := json.Marshal(s); return string(b) }
