package config

import (
	"encoding/json"
	"testing"
)

func TestChainKeepsReferencesAndRejectsNestedOrDeletedMembers(t *testing.T) {
	c := NewCatalog()
	a := RouteConfig{NodeProfile: NodeProfile{Name: "A", ProtocolName: "trojan", Server: "example.com", Port: 443}, SchemaVersion: 1, Password: "fixture", Security: "tls", Transport: "tcp", ServerName: "example.com"}
	a.ID = ""
	a.Name = "A"
	b := a
	b.Name = "B"
	b.Server = "other.example.com"
	aid, _, err := c.Add(a)
	if err != nil {
		t.Fatal(err)
	}
	bid, _, err := c.Add(b)
	if err != nil {
		t.Fatal(err)
	}
	chain, err := c.SaveChain("", "Pair", aid, bid, 0, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(aid, ""); err == nil {
		t.Fatal("deleted referenced front")
	}
	if _, err := c.SaveChain("", "nested", chain.Config.ID, bid, 0, chain.Revision, 1); err == nil {
		t.Fatal("nested chain accepted")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var round Catalog
	if err = json.Unmarshal(raw, &round); err != nil {
		t.Fatal(err)
	}
	r, ok := round.Find(chain.Config.ID)
	if !ok || r.Chain == nil || r.Chain.FrontID != aid {
		t.Fatal("chain references lost")
	}
}
