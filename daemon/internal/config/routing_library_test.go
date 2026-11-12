package config

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

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
	if _, err = CompileRouting(l, nil, nil, true); err == nil {
		t.Fatal("missing route silently fell back")
	}
}
