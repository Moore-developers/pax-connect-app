package telemetry_test

import (
	"pax-connect/daemon/internal/telemetry"
	"strings"
	"testing"
)

func TestRawDiagnosticCredentialsNeverSurvive(t *testing.T) {
	for _, raw := range []string{"Authorization: Bearer private-token", "proxy: https://user:pass@node.example/path?token=private-token", "password=private-token", `{"uuid":"private-token"}`, `{"token":"private-token"}`, `{"secret":"private-token"}`, `unknown format private-token`} {
		safe, _, _ := telemetry.ScrubRaw(raw)
		if strings.Contains(safe, "private-token") || strings.Contains(safe, "user:pass") {
			t.Fatalf("credential leaked: %q", safe)
		}
	}
}

func TestDiagnosticScrubbingIsIdempotent(t *testing.T) {
	for _, input := range []string{"Authorization: Bearer private-token", "proxy: https://user:pass@node.example/path?token=secret", "connection: read tcp 192.0.2.1:80: i/o timeout"} {
		first, truncated, omitted := telemetry.ScrubRaw(input)
		second, again, lost := telemetry.ScrubRaw(first)
		if first != second || truncated != again || omitted != lost {
			t.Fatalf("scrubbing not stable: %q -> %q -> %q (%s/%s)", input, first, second, omitted, lost)
		}
	}
}
