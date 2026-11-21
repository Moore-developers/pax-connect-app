package config

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestClaudePackagePersistsWhileLegacyReadersKeepExistingPackages(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "routing.json")
	store := NewRoutingLibraryStore(path, uint32(os.Getuid()))
	library := NewRoutingLibrary()
	custom := RoutingPackage{ID: "mine", Name: "Claude", Kind: "local", Destination: "direct", Enabled: true}
	library.Packages = slices.Insert(library.Packages, 1, custom)
	claude := NewClaudeRoutingPackage()
	claude.Enabled, claude.Destination = true, "route:fixed"
	library.Packages = slices.Insert(library.Packages, library.BuiltInStart(), claude)
	if err := store.Save(context.Background(), library); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := store.Load()
	if err != nil || !reflect.DeepEqual(loaded, library) {
		t.Fatalf("readback: %v, %v", loaded, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var legacy RoutingLibrary
	if err := json.Unmarshal(raw, &legacy); err != nil || legacy.Validate() != nil ||
		len(legacy.Packages) != 3 || !reflect.DeepEqual(legacy.Packages[1], custom) {
		t.Fatalf("legacy package list: %v, %v", legacy, err)
	}
}

func TestRoutingLibraryOfflinePersistenceAndValidation(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(dir, 0700)
	s := NewRoutingLibraryStore(filepath.Join(dir, "routing.json"), uint32(os.Getuid()))
	l, exists, err := s.Load()
	if err != nil || exists || l.Validate() != nil {
		t.Fatal(l, exists, err)
	}
	p := RoutingPackage{ID: "mine", Name: "我的服务", Kind: "local", Destination: "direct", Enabled: true, Rules: []RoutingEntry{{ID: "a", Method: "domain", Target: "example.com", Destination: "direct", Enabled: true}}}
	l.Packages = append(l.Packages[:1], append([]RoutingPackage{p}, l.Packages[1:]...)...)
	l.Revision++
	if err := s.Save(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := s.Load()
	if err != nil || loaded.Packages[1].Rules[0].Target != "example.com" {
		t.Fatal(loaded, err)
	}
	l.Packages[1].Rules[0].Target = "https://bad/path"
	if s.Save(context.Background(), l) == nil {
		t.Fatal("invalid target saved")
	}
	again, _, _ := s.Load()
	if again.Packages[1].Rules[0].Target != "example.com" {
		t.Fatal("bad save overwrote valid state")
	}
}
func TestCompileRoutingHonorsPriorityDefaultAndOfflineTargets(t *testing.T) {
	l := NewRoutingLibrary()
	l.Packages[0].Rules = []RoutingEntry{{ID: "default", Method: "domain", Target: "example.com", Destination: "default", Enabled: true}, {ID: "block", Method: "domain_suffix", Target: "example.com", Destination: "block", Enabled: true}}
	l.Packages = append(l.Packages[:1], append([]RoutingPackage{{ID: "mine", Name: "mine", Kind: "local", Destination: "route:node", Enabled: true, Rules: []RoutingEntry{{ID: "local", Method: "domain", Target: "example.com", Destination: "direct", Enabled: true}}}}, l.Packages[1:]...)...)
	rules, err := CompileRouting(l, nil, map[string]string{"route:node": "node-tag"}, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(rules)
	t.Log(string(raw))
	if rules[2]["type"] != "logical" || rules[3]["outbound"] != "node-tag" {
		t.Fatal(rules)
	}
	l.RouteNames = map[string]string{"route:node": "线路 A"}
	for _, tags := range []map[string]string{nil, {"route:node": ""}} {
		rejected, labels, err := CompileRoutingEvidence(l, nil, tags, true)
		if err != nil || len(rejected) != len(rules) || len(labels) != len(rules) {
			t.Fatal("missing route broke the rule table", rejected, labels, err)
		}
		want := map[string]any{"domain": []string{"example.com"}, "action": "reject", "no_drop": true}
		if !reflect.DeepEqual(rejected[3], want) {
			t.Fatalf("missing route lost its original predicate or terminal reject: %v", rejected[3])
		}
		evidence, ok := RecordedRoutingEvidence(labels[3])
		if !ok || evidence.Package != "mine" || evidence.Target != "example.com" || evidence.UnavailableRoute != "线路 A" {
			t.Fatalf("expected unavailable route and original rule evidence: %+v", evidence)
		}
		if !reflect.DeepEqual(rejected[:3], rules[:3]) || !reflect.DeepEqual(rejected[4:], rules[4:]) {
			t.Fatal("missing route changed unrelated rules")
		}
	}
	if l.Packages[1].Destination != "route:node" {
		t.Fatal("rejection changed the saved destination", l.Packages[1].Destination)
	}
}
