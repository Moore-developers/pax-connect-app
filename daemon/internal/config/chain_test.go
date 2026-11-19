package config

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

func TestCatalogKeepsChainWhenSourceBecomesTCPOnly(t *testing.T) {
	c := NewCatalog()
	source, _, err := c.UpsertSubscription("Fixture", "https://sub.example.test/")
	if err != nil {
		t.Fatal(err)
	}
	front := RouteConfig{NodeProfile: NodeProfile{Name: "Front", ProtocolName: "shadowsocks", Server: "front.example.test", Port: 8388}, SchemaVersion: 1, Password: "fixture", Method: "aes-128-gcm", Security: "none", Transport: "tcp"}
	last := RouteConfig{NodeProfile: NodeProfile{Name: "Last", ProtocolName: "hysteria2", Server: "last.example.test", Port: 443}, SchemaVersion: 1, Password: "fixture", Security: "tls", ServerName: "last.example.test", Transport: "quic"}
	frontID, _, err := c.AddFromSource(front, source.ID, front.Name, false)
	if err != nil {
		t.Fatal(err)
	}
	lastID, _, err := c.Add(last)
	if err != nil {
		t.Fatal(err)
	}
	chain, err := c.SaveChain("", "Pair", frontID, lastID, 0, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	front, err = RouteFromDocument([]byte(`{"type":"shadowsocks","tag":"Front","server":"front.example.test","server_port":8388,"method":"aes-128-gcm","password":"fixture","network":"tcp"}`), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.MergeSource(source.ID, []SourceNode{{Name: front.Name, Config: front}}, true); err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store := NewCatalogStore(filepath.Join(dir, "routes.json"), uint32(os.Geteuid()))
	if err = store.Save(context.Background(), c); err != nil {
		t.Fatal("could not save catalog after refresh", err)
	}
	loaded, _, err := store.Load()
	if err != nil {
		t.Fatal("catalog could not be reopened", err)
	}
	member, _ := loaded.Find(frontID)
	pair, ok := loaded.Find(chain.Config.ID)
	if !ok || pair.Chain.FrontID != frontID || member.ConfigRevision <= 1 {
		t.Fatal("chain reference or refreshed member version was lost")
	}
	if _, err := loaded.SaveChain("", "New pair", frontID, lastID, 0, member.Revision, 1); err == nil || err.Error() != "chain_udp_required" {
		t.Fatal("TCP-only front accepted for a new QUIC combination", err)
	}
}
