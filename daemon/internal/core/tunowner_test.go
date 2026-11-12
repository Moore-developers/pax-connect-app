package core

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestTUNOwnerClassification(t *testing.T) {
	for _, tt := range []struct {
		name, ifconfig string
		holders        []process
		want           tunOwner
	}{
		{"NE", "agent domain:NetworkExtension", nil, tunOwner{Kind: "network_extension", Name: "Fixture VPN", BundleID: "com.example.vpn"}},
		{"process app ancestor", "utun61: flags", []process{{PID: 100, Parent: 99, Path: "/opt/core"}}, tunOwner{Kind: "process", Name: "Fixture", BundleID: "com.example.fixture", PID: 100, Path: "/opt/core"}},
		{"standalone", "utun61: flags", []process{{PID: 100, Parent: 1, Path: "/opt/core"}}, tunOwner{Kind: "process", Name: "core", PID: 100, Path: "/opt/core"}},
		{"unknown", "utun61: flags", nil, tunOwner{Kind: "unknown"}},
		{"ambiguous", "utun61: flags", []process{{PID: 100}, {PID: 101}}, tunOwner{Kind: "unknown"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := tunHost{
				run: func(_ context.Context, name string, args ...string) (string, error) {
					if name == "/sbin/ifconfig" {
						return tt.ifconfig, nil
					}
					if name == "/usr/sbin/scutil" && reflect.DeepEqual(args, []string{"--nc", "show", "fixture-id"}) {
						return "NEProviderBundleIdentifier : com.example.vpn\nUserDefinedName : Fixture VPN\n", nil
					}
					t.Fatalf("unexpected command %s %v", name, args)
					return "", nil
				},
				scutil: func(_ context.Context, input string) (string, error) {
					if strings.HasPrefix(input, "list") {
						return "subKey [0] = State:/Network/Service/other/DNS\nsubKey [1] = State:/Network/Service/fixture-id/DNS\n", nil
					}
					if strings.Contains(input, "other") {
						return "InterfaceName : utun9", nil
					}
					return "InterfaceName : utun61", nil
				},
				scan: func(_ context.Context, unit int) ([]process, error) {
					if unit != 62 {
						t.Fatalf("unit %d", unit)
					}
					return tt.holders, nil
				},
				process: func(pid int) process {
					if pid != 99 {
						t.Fatal(pid)
					}
					return process{PID: 99, Parent: 1, Path: "/Applications/Fixture.app/Contents/MacOS/Fixture"}
				},
				app: func(_ context.Context, path string) (string, string) {
					if path != "/Applications/Fixture.app" {
						t.Fatal(path)
					}
					return "Fixture", "com.example.fixture"
				},
			}
			got, err := identifyOwner(context.Background(), "utun61", h)
			if err != nil || got != tt.want {
				t.Fatalf("%+v %v want %+v", got, err, tt.want)
			}
		})
	}
}
func TestTUNOwnerBoundsAndErrors(t *testing.T) {
	for _, name := range []string{"en0", "utun1;bad", "utun-1", "utun1\n"} {
		if validUTUN(name) {
			t.Fatal(name)
		}
	}
	count := 0
	h := tunHost{run: func(context.Context, string, ...string) (string, error) { return "plain", nil }, scan: func(context.Context, int) ([]process, error) {
		return []process{{PID: 20, Parent: 19, Path: "/core"}}, nil
	}, process: func(pid int) process { count++; return process{PID: pid, Parent: pid - 1, Path: "/parent"} }}
	got, err := identifyOwner(context.Background(), "utun1", h)
	if err != nil || got.Kind != "process" || count > 5 {
		t.Fatalf("%+v %v count %d", got, err, count)
	}
	h.run = func(context.Context, string, ...string) (string, error) { return "", errors.New("fixture unavailable") }
	if _, err = identifyOwner(context.Background(), "utun1", h); err == nil {
		t.Fatal("lost OS error")
	}
}
