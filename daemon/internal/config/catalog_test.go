package config

import "testing"

func TestCatalogDedupSelectionAndDeletion(t *testing.T) {
	c := NewCatalog()
	p := RouteConfig{NodeProfile: NodeProfile{Name: "HY", ProtocolName: "hysteria2", Server: "example.com", Port: 443}, SchemaVersion: 1, Password: "secret", ServerName: "example.com", Transport: "quic", Security: "tls"}
	id, added, err := c.Add(p)
	if err != nil || !added {
		t.Fatal(err)
	}
	duplicate, added, err := c.Add(p)
	if err != nil || added || duplicate != id {
		t.Fatal("duplicate")
	}
	if err := c.Select(id); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(id, id); err == nil {
		t.Fatal("active deleted")
	}
	if err := c.Delete(id, ""); err != nil {
		t.Fatal(err)
	}
	if len(c.Routes) != 0 || c.SelectedID != "" {
		t.Fatal("deleted selected not cleared")
	}
}
