package network

import (
	"context"
	"errors"
	"testing"
)

func TestRouteParsingUsesActualInterface(t *testing.T) {
	if ParseRouteInterface("   route to: 1.1.1.1\n  interface: utun17\n") != "utun17" {
		t.Fatal("route parse")
	}
	if ParseRouteInterface("route: not in table") != "" {
		t.Fatal("invented route")
	}
}

func TestClassifyConflictDistinguishesOwnershipAndUnknown(t *testing.T) {
	if got := ClassifyConflict(map[string]string{"1.1.1.1": "utun9"}, "utun9", true); got.Kind != ConflictPax {
		t.Fatal("pax interface was classified as another utun", got)
	}
	if got := ClassifyConflict(map[string]string{"1.1.1.1": "utun7"}, "utun9", true); got.Kind != ConflictOtherUTUN || got.Target != "1.1.1.1" {
		t.Fatal("external utun was not preserved", got)
	}
	if got := ClassifyConflict(map[string]string{"1.1.1.1": "utun9", "8.8.8.8": "utun7"}, "utun9", true); got.Kind != ConflictOtherUTUN || got.Interface != "utun7" {
		t.Fatal("external utun did not take priority over Pax's stale interface", got)
	}
	if got := ClassifyConflict(map[string]string{"1.1.1.1": ""}, "utun9", true); got.Kind != ConflictUnknown {
		t.Fatal("empty route interface was silently treated as safe", got)
	}
	if got := ClassifyConflict(nil, "", false); got.Kind != ConflictObservationError {
		t.Fatal("observation failure was silently treated as no conflict", got)
	}
}

func TestConflictObservationAllowsAbsentIPv6ButNotFailedLookup(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want ConflictKind
	}{
		{"IPv4 only", ErrNoRoute, ConflictNone},
		{"query failed", errors.New("unavailable"), ConflictObservationError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := assessConflict(context.Background(), func(_ context.Context, target string) (string, error) {
				if target == "2606:4700:4700::1111" {
					return "", tc.err
				}
				return "en0", nil
			}, func() (string, error) { return "", nil })
			if result.Kind != tc.want {
				t.Fatal(result)
			}
		})
	}
}
