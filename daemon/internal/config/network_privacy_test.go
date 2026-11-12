package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestNetworkPrivacyPersistenceAndMalformedStore(t *testing.T) {
	dir, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	_ = os.Chmod(dir, 0700)
	store := NewNetworkPrivacyStore(filepath.Join(dir, "privacy.json"), uint32(os.Geteuid()))
	p, exists, e := store.Load()
	if e != nil || exists || p.DNSMode != "balanced" || p.WebRTC {
		t.Fatal(p, exists, e)
	}
	p.DNSMode = "strict"
	p.WebRTC = true
	if e = store.Save(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	got, exists, e := store.Load()
	if e != nil || !exists || got != p {
		t.Fatal("restart changed policy")
	}
	_ = os.WriteFile(filepath.Join(dir, "privacy.json"), []byte(`{"revision":1,"dns_mode":"invalid"}`), 0600)
	if _, _, e = store.Load(); e == nil {
		t.Fatal("corruption silently defaulted")
	}
}
func TestWebRTCEnforcementKeepsRuleLabelsAligned(t *testing.T) {
	l := NewRoutingLibrary()
	l.WebRTC = true
	rules, labels, e := CompileRoutingEvidence(l, nil, nil, true)
	if e != nil || len(rules) != len(labels) {
		t.Fatal(e)
	}
	if rules[0]["action"] != "sniff" || rules[1]["outbound"] != "proxy" || rules[1]["type"] != "logical" {
		t.Fatal("STUN enforcement not before direct", rules)
	}
}
