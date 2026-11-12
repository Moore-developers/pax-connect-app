package singbox

import (
	"context"
	"strings"
	"testing"
)

// The new upstream list-valued tag must not widen root's two fixed rule-set paths.
func TestKernelUpgradeRejectsMultiTagAndNewDialerCapabilities(t *testing.T) {
	for _, raw := range []string{
		`{"route":{"rule_set":[{"type":"local","tag":["geosite-cn","geoip-cn"],"format":"binary","path":"/tmp/untrusted"}]}}`,
		`{"outbounds":[{"type":"direct","tag":"direct","protect_path":"/tmp/untrusted"}]}`,
		`{"outbounds":[{"type":"direct","tag":"direct","netns":"untrusted"}]}`,
	} {
		if _, err := ValidateCoreOptions(context.Background(), []byte(raw), t.TempDir(), 17893); err == nil {
			t.Fatal("upstream field expansion bypassed root capability boundary")
		}
	}
	canonical, err := ValidateCoreOptions(context.Background(), []byte(`{"route":{"rule_set":[{"type":"local","tag":"geosite-cn","format":"binary","path":"/tmp/untrusted"}]}}`), t.TempDir(), 17893)
	if err != nil || strings.Contains(canonical, "/tmp/untrusted") {
		t.Fatal("single allowed rule-set no longer rewrites to protected root path", err)
	}
}
