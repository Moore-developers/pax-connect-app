package config

import (
	"strings"
	"testing"
)

func TestSafeDocumentSecretsRoundTrip(t *testing.T) {
	previous := []byte(`{"type":"vless","server":"edge.test","server_port":443,"uuid":"PRIVATE-UUID","transport":{"type":"ws","path":"/PRIVATE-PATH","headers":{"Host":"cdn.test","Authorization":"PRIVATE-HEADER","X/A~B":["PRIVATE-ARRAY"]}},"tls":{"enabled":true,"server_name":"edge.test","reality":{"short_id":"PRIVATE-SHORT"}},"connect_timeout":"7s"}`)
	redacted, credentials, err := RedactRouteDocument(previous)
	if err != nil || strings.Contains(string(redacted), "PRIVATE") {
		t.Fatalf("unsafe read: %s %v", redacted, err)
	}
	if len(credentials) < 8 {
		t.Fatal("missing presence paths")
	}
	value := "NEW-UUID"
	merged, err := MergeRouteSecrets(redacted, previous, []DocumentSecretUpdate{{Path: "/uuid", Value: &value}, {Path: "/transport/headers/X~1A~0B", Clear: true}})
	if err != nil || !strings.Contains(string(merged), "NEW-UUID") || !strings.Contains(string(merged), "PRIVATE-PATH") || !strings.Contains(string(merged), "PRIVATE-HEADER") || strings.Contains(string(merged), "PRIVATE-ARRAY") {
		t.Fatalf("merge failed: %s %v", merged, err)
	}
	// Non-secret omission is a real removal; absent containers do not resurrect.
	clean := []byte(`{"type":"socks","server":"127.0.0.1","server_port":9}`)
	merged, err = MergeRouteSecrets(clean, previous, nil)
	if err != nil || strings.Contains(string(merged), "PRIVATE") || strings.Contains(string(merged), "connect_timeout") {
		t.Fatalf("cross protocol preservation: %s %v", merged, err)
	}
}
func TestSafeDocumentRejectsAmbiguousSecretChanges(t *testing.T) {
	base := []byte(`{"type":"trojan","server":"edge.test","server_port":443}`)
	value := "secret"
	for _, updates := range [][]DocumentSecretUpdate{
		{{Path: "/password"}}, {{Path: "/password", Value: &value, Clear: true}}, {{Path: "/password", Clear: true}}, {{Path: "/tls/__proto__/password", Value: &value}}, {{Path: "/password", Value: &value}, {Path: "/password", Value: &value}},
	} {
		if _, err := MergeRouteSecrets(base, nil, updates); err == nil {
			t.Fatalf("invalid update accepted: %#v", updates)
		}
	}
	for _, raw := range []string{
		`{"type":"http","password":"secret"}`, `{"type":"http","headers":{"Authorization":"secret"}}`,
		`{"type":"http","tls":{"new_token":"secret"}}`, `{"type":"http","client_metadata":"secret"}`,
	} {
		if _, err := MergeRouteSecrets([]byte(raw), nil, nil); err == nil {
			t.Fatalf("inline/unknown secret accepted: %s", raw)
		}
	}
	cross := []byte(`{"type":"vless","server":"edge.test","server_port":443,"uuid":"secret"}`)
	if _, err := MergeRouteSecrets(base, cross, nil); err == nil || err.Error() != "credentials_required" {
		t.Fatalf("cross protocol borrowed credential: %v", err)
	}
}
