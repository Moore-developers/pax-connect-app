package network

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

func TestPathDiagnosticsSeparatesSettingsSocketsAndRouting(t *testing.T) {
	var dials []string
	inspector := PathInspector{Command: func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "/usr/sbin/scutil":
			return []byte("<dictionary> {\n  HTTPEnable : 1\n  HTTPProxy : 127.0.0.1\n  HTTPPort : 10808\n  ProxyAutoConfigEnable : 1\n  ProxyAutoConfigURLString : https://secret:password@example.test/pac?token=private\n  __SCOPED__ : <dictionary> {\n    en9 : <dictionary> {\n      HTTPPort : 9999\n    }\n  }\n}"), nil
		case "/bin/ps":
			if len(args) > 0 && args[0] == "-ww" {
				return []byte("12 /Applications/Google Chrome.app/Contents/MacOS/Google Chrome --proxy-server=http://user:secret@127.0.0.1:9999 --other=private\n13 /Applications/Firefox.app/Contents/MacOS/firefox -profile private"), nil
			}
			return []byte("12 /Applications/Google Chrome.app/Contents/MacOS/Google Chrome\n13 /Applications/Firefox.app/Contents/MacOS/firefox"), nil
		case "/usr/sbin/netstat":
			return []byte("tcp4 0 0 127.0.0.1.10808 *.* LISTEN 0 0 0 0 sing-box:12 0\ntcp4 0 0 127.0.0.1.50000 127.0.0.1.10808 ESTABLISHED 0 0 0 0 Google Chrome He:13 0"), nil
		case "/sbin/route":
			if strings.Contains(strings.Join(args, " "), "inet6") {
				return []byte(" interface: utun9"), nil
			}
			return []byte(" interface: utun7"), nil
		}
		return nil, errors.New("unexpected command")
	}, Dial: func(_ context.Context, _, endpoint string) (net.Conn, error) {
		dials = append(dials, endpoint)
		return nil, errors.New("refused")
	}}
	report := inspector.Inspect(context.Background(), "utun7")
	find := func(kind, subject, state, value string) {
		t.Helper()
		for _, f := range report.Findings {
			if f == (PathFinding{kind, subject, state, value}) {
				return
			}
		}
		t.Fatalf("missing %s/%s/%s/%s in %+v", kind, subject, state, value, report)
	}
	find("system", "HTTP", "enabled", "127.0.0.1:10808")
	find("system", "ProxyAutoConfig", "dynamic", "")
	find("proxy_port", "HTTP", "unreachable", "127.0.0.1:10808")
	find("listener", "HTTP", "observed", "sing-box")
	find("browser", "Google Chrome", "proxy_override", "127.0.0.1:9999")
	find("browser", "Firefox", "not_observed", "")
	find("browser_socket", "Chrome", "observed", "127.0.0.1:10808")
	find("route", "1.1.1.1", "pax", "utun7")
	find("route", "2606:4700:4700::1111", "other", "utun9")
	if len(dials) != 1 || dials[0] != "127.0.0.1:10808" {
		t.Fatal("diagnostics dialed an unconfigured remote endpoint", dials)
	}
	for _, f := range report.Findings {
		if strings.Contains(f.Value, "private") || strings.Contains(f.Value, "secret") || strings.Contains(f.Value, "password") {
			t.Fatal("private arguments/PAC URL escaped")
		}
	}
}

func TestPathDiagnosticsUnavailableIsUnknown(t *testing.T) {
	inspector := PathInspector{Command: func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("unavailable") }, Dial: func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("dial after failed settings")
		return nil, nil
	}}
	report := inspector.Inspect(context.Background(), "")
	if !report.Partial {
		t.Fatal("missing partial marker")
	}
	for _, f := range report.Findings {
		if f.State == "pax" || f.State == "disabled" {
			t.Fatal("missing observation was treated as success", f)
		}
	}
}

func TestPathDiagnosticsDisabledIPv6IsNotUnknownRoute(t *testing.T) {
	ipv6Lookups := 0
	inspector := PathInspector{
		Physical: func(context.Context) (Physical, error) { return Physical{Name: "en0", IPv6State: "link_local"}, nil },
		Command: func(_ context.Context, name string, args ...string) ([]byte, error) {
			switch name {
			case "/usr/sbin/scutil":
				return []byte("<dictionary> {\n}"), nil
			case "/sbin/route":
				if strings.Contains(strings.Join(args, " "), "inet6") {
					ipv6Lookups++
					return nil, ErrNoRoute
				}
				return []byte("interface: utun7"), nil
			default:
				return nil, nil
			}
		},
	}
	report := inspector.Inspect(context.Background(), "utun7")
	disabled, ipv4 := false, false
	for _, finding := range report.Findings {
		if finding.Kind == "ip_family" && finding.Subject == "ipv6" && finding.State == "link_local" {
			disabled = true
		}
		if finding.Kind == "route" && finding.Subject == "1.1.1.1" && finding.State == "pax" {
			ipv4 = true
		}
		if finding.Kind == "route" && finding.Subject == "2606:4700:4700::1111" {
			t.Fatal("disabled IPv6 was still probed")
		}
	}
	if !disabled || !ipv4 || ipv6Lookups != 0 {
		t.Fatal(report, ipv6Lookups)
	}
}
