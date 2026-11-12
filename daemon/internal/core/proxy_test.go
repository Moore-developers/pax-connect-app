package core

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestProxyCleanupDecisions(t *testing.T) {
	for _, tt := range []struct {
		name, enabled, host, port string
		clear                     bool
	}{
		{"Pax", "Yes", "127.0.0.1", "17893", true}, {"disabled", "No", "127.0.0.1", "17893", false}, {"other port", "Yes", "127.0.0.1", "10808", false}, {"other host", "Yes", "192.0.2.1", "17893", false}, {"localhost untouched", "Yes", "localhost", "17893", false}, {"malformed", "", "", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var writes []string
			run := func(_ context.Context, _ string, args ...string) (string, error) {
				if args[0] == "-listallnetworkservices" {
					return "An asterisk (*) denotes disabled services.\nWi-Fi\n*Ethernet\n", nil
				}
				if strings.HasPrefix(args[0], "-get") {
					return "Enabled: " + tt.enabled + "\nServer: " + tt.host + "\nPort: " + tt.port + "\n", nil
				}
				writes = append(writes, strings.Join(args, " "))
				return "", nil
			}
			if err := cleanupProxy(context.Background(), 17893, run); err != nil {
				t.Fatal(err)
			}
			var want []string
			if tt.clear {
				for _, service := range []string{"Wi-Fi", "Ethernet"} {
					for _, kind := range []string{"webproxy", "securewebproxy", "socksfirewallproxy"} {
						want = append(want, "-set"+kind+"state "+service+" off")
					}
				}
			}
			if !reflect.DeepEqual(writes, want) {
				t.Fatalf("%v != %v", writes, want)
			}
		})
	}
	if err := cleanupProxy(context.Background(), 17893, func(context.Context, string, ...string) (string, error) { return "", errors.New("test") }); err == nil {
		t.Fatal("ignored failure")
	}
}

func TestProxyCleanupMixedServices(t *testing.T) {
	var writes []string
	run := func(_ context.Context, _ string, args ...string) (string, error) {
		if args[0] == "-listallnetworkservices" {
			return "An asterisk (*) denotes disabled services.\nUnreadable\nPax\nThirdParty\nPAC\n", nil
		}
		if strings.HasPrefix(args[0], "-get") {
			switch args[1] {
			case "Unreadable":
				return "", errors.New("fixture read denied")
			case "Pax":
				if args[0] == "-getwebproxy" {
					return "Enabled: Yes\nServer: 127.0.0.1\nPort: 17893\n", nil
				}
			case "ThirdParty":
				return "Enabled: Yes\nServer: 127.0.0.1\nPort: 10808\n", nil
			case "PAC":
				return "Enabled: No\nURL: https://example.invalid/pac\n", nil
			}
			return "Enabled: No\n", nil
		}
		writes = append(writes, strings.Join(args, " "))
		return "", nil
	}
	if err := cleanupProxy(context.Background(), 17893, run); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(writes, []string{"-setwebproxystate Pax off"}) {
		t.Fatal(writes)
	}
}
