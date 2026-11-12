package network

import (
	"context"
	"errors"
	"testing"
)

func TestPhysicalIPv6Classification(t *testing.T) {
	for _, tc := range []struct {
		name, mode     string
		global, routed bool
		err            error
		want           string
	}{
		{"off", "disabled", false, false, nil, "disabled"},
		{"local only", "link_local", false, false, nil, "link_local"},
		{"automatic no address", "automatic", false, false, nil, "unavailable"},
		{"automatic no route", "automatic", true, false, ErrNoRoute, "unavailable"},
		{"dual stack", "automatic", true, true, nil, "available"},
		{"observation failure", "automatic", true, false, errors.New("timeout"), "unknown"},
		{"contradiction", "disabled", true, true, nil, "unknown"},
		{"no setup evidence", "unknown", false, false, nil, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyIPv6(tc.mode, tc.global, tc.routed, tc.err); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestIPv4OnlyPhysicalRejectsIPv6BeforeDial(t *testing.T) {
	_, err := DialPhysical(context.Background(), Physical{Name: "en0", Index: 1, IPv6State: "link_local"}, "tcp", "[2606:4700:4700::1111]:443")
	if !errors.Is(err, ErrIPv6Unavailable) {
		t.Fatal(err)
	}
}
