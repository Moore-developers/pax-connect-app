package singbox

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/srs"
	"github.com/sagernet/sing-box/option"
	"os"
	"path/filepath"
	"testing"
)

func TestRoutingSourceJSONAndSRSKeepPredicates(t *testing.T) {
	raw := []byte(`{"version":3,"rules":[{"domain_suffix":["example.com"]},{"ip_cidr":["192.0.2.0/24"]}]}`)
	entries, _, err := DecodeRoutingSource(raw)
	if err != nil || len(entries) != 2 {
		t.Fatal(entries, err)
	}
	var set option.PlainRuleSetCompat
	if json.Unmarshal(raw, &set) != nil {
		t.Fatal("fixture")
	}
	plain, err := set.Upgrade()
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	if err := srs.Write(&compressed, plain, 3); err != nil {
		t.Fatal(err)
	}
	imported, _, err := DecodeRoutingSource(compressed.Bytes())
	if err != nil || len(imported) != 2 || !bytes.Contains(imported[0].Match, []byte("example.com")) || !bytes.Contains(imported[1].Match, []byte("192.0.2.0/24")) {
		t.Fatal(imported, err)
	}
	for _, invalid := range []string{`{"version":3,"rules":[{"domain":["example.com"],"action":"reject"}]}`, `{"version":3,"rules":[{"rule_set":["arbitrary"]}]}`, `{"version":3,"rules":[{"unknown_field":true}]}`} {
		if _, _, e := DecodeRoutingSource([]byte(invalid)); e == nil {
			t.Fatal("unsafe source admitted", invalid)
		}
	}
}
func TestRoutingUpdateKeepsInstanceAndRejectsTransportChanges(t *testing.T) {
	r := NewCoreRuntime(context.Background())
	defer r.Close()
	original := `{"log":{"disabled":true},"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct","find_process":true,"rules":[{"domain":["old.example.com"],"action":"reject"}]}}`
	var doc map[string]any
	if err := json.Unmarshal([]byte(original), &doc); err != nil {
		t.Fatal(err)
	}
	doc["experimental"] = map[string]any{"cache_file": map[string]any{"enabled": true, "path": filepath.Join(t.TempDir(), "cache.db")}}
	originalBytes, _ := json.Marshal(doc)
	original = string(originalBytes)
	if err := r.Apply(original); err != nil {
		t.Fatal(err)
	}
	instance := r.current().Instance()
	session, version := r.CurrentPathIdentity()
	updated := `{"log":{"disabled":true},"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct","find_process":true,"rules":[{"domain":["new.example.com"],"action":"reject"}]}}`
	var nextDoc map[string]any
	_ = json.Unmarshal([]byte(updated), &nextDoc)
	nextDoc["experimental"] = doc["experimental"]
	updatedBytes, _ := json.Marshal(nextDoc)
	updated = string(updatedBytes)
	if err := r.UpdateRouting(context.Background(), updated); err != nil {
		t.Fatal(err)
	}
	if r.current().Instance() != instance || r.Status() != "STARTED" {
		t.Fatal("update reloaded instance")
	}
	nextSession, _ := r.CurrentPathIdentity()
	if nextSession != session {
		t.Fatal("update replaced runtime session", version)
	}
	rules := instance.Box().Router().Rules()
	metadata := adapter.InboundContext{Domain: "new.example.com"}
	if len(rules) != 1 || !rules[0].Match(&metadata) {
		t.Fatal("new rules not applied")
	}
	invalid := bytes.ReplaceAll([]byte(updated), []byte(`"type":"direct"`), []byte(`"type":"block"`))
	if r.UpdateRouting(context.Background(), string(invalid)) == nil {
		t.Fatal("transport mutation admitted")
	}
	if r.current().Instance() != instance || len(instance.Box().Router().Rules()) != 1 {
		t.Fatal("failed update lost current instance")
	}
	if err := r.UpdateRouting(context.Background(), bytes.NewBufferString(updated).String()); err != nil {
		t.Fatal("idempotent retry", err)
	}
}

func TestBundledGeoSourcesAreBrowsableAndCompileWithFrozenLabels(t *testing.T) {
	for _, tag := range []string{"geosite-cn", "geoip-cn"} {
		raw, err := os.ReadFile(filepath.Join("../../../rulesets", tag+".srs"))
		if err != nil {
			t.Fatal(err)
		}
		entries, version, err := DecodeRoutingSource(raw)
		if err != nil || len(entries) == 0 {
			t.Fatal(tag, version, err)
		}
		t.Logf("%s groups=%d version=%s", tag, len(entries), version)
	}
}
