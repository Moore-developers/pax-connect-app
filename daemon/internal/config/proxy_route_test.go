package config

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPurchasedProxyPersistenceAndLocalIdentity(t *testing.T) {
	for _, kind := range []string{"socks", "http", "https"} {
		t.Run(kind, func(t *testing.T) {
			typ, tls := kind, ""
			if kind == "https" {
				typ = "http"
				tls = `,"tls":{"enabled":true}`
			}
			raw := `{"type":"` + typ + `","server":"proxy.example","server_port":1080,"username":"user","password":"p:@%"` + tls + `}`
			p, err := RouteFromDocument([]byte(raw), "", "Purchased")
			if err != nil {
				t.Fatal(err)
			}
			catalog := NewCatalog()
			id, created, err := catalog.Add(p)
			if err != nil || !created {
				t.Fatalf("add: %v", err)
			}
			duplicate, created, err := catalog.Add(p)
			if err != nil || created || duplicate != id {
				t.Fatal("local duplicate not retained")
			}
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			store := NewCatalogStore(filepath.Join(dir, "routes.json"), uint32(os.Getuid()))
			if err := store.Save(context.Background(), catalog); err != nil {
				t.Fatal(err)
			}
			loaded, exists, err := store.Load()
			if err != nil || !exists || len(loaded.Routes) != 1 {
				t.Fatalf("reload: %v", err)
			}
			if loaded.Routes[0].Config.Username != "user" || loaded.Routes[0].Config.Password != "p:@%" {
				t.Fatal("credentials lost")
			}
			var obj map[string]any
			if err := json.Unmarshal(loaded.Routes[0].Config.Document, &obj); err != nil {
				t.Fatal(err)
			}
			delete(obj, "username")
			delete(obj, "password")
			clean, _ := json.Marshal(obj)
			anonymous, err := RouteFromDocument(clean, id, "Anonymous")
			if err != nil || anonymous.Username != "" || anonymous.Password != "" {
				t.Fatal("anonymous edit invalid")
			}
			if kind == "https" && anonymous.ServerName != "proxy.example" {
				t.Fatal("default certificate name lost")
			}
		})
	}
}

func TestPurchasedProxyRejectsPartialOrUnsupportedAuthentication(t *testing.T) {
	for _, raw := range []string{
		`{"type":"socks","server":"host","server_port":1080,"username":"user"}`,
		`{"type":"socks","server":"host","server_port":1080,"version":"4"}`,
		`{"type":"socks","server":"host","server_port":1080,"version":4}`,
		`{"type":"http","server":"host","server_port":1080,"tls":{"enabled":true,"server_name":42}}`,
		`{"type":"socks","server":"host","server_port":1080,"username":"u","password":"` + strings.Repeat("x", 256) + `"}`,
		`{"type":"http","server":"host","server_port":1080,"username":"u:bad","password":"p"}`,
	} {
		if _, err := RouteFromDocument([]byte(raw), "", "Invalid"); err == nil {
			t.Fatal("invalid proxy accepted")
		}
	}
}

func TestPurchasedProxyCanBeIndependentFromSubscription(t *testing.T) {
	p, err := RouteFromDocument([]byte(`{"type":"socks","server":"proxy.example","server_port":1080}`), "", "Proxy")
	if err != nil {
		t.Fatal(err)
	}
	catalog := NewCatalog()
	catalog.Subscriptions = []SubscriptionRecord{{ID: "provider", Name: "Provider", URL: "https://provider.example", Revision: 1}}
	source, _, err := catalog.AddFromSource(p, "provider", "Proxy", false)
	if err != nil {
		t.Fatal(err)
	}
	local, created, err := catalog.Add(p)
	if err != nil || !created || local == source || len(catalog.Routes) != 2 {
		t.Fatal("local proxy tied to subscription")
	}
	duplicate, created, err := catalog.Add(p)
	if err != nil || created || duplicate != local {
		t.Fatal("local duplicate selected provider")
	}
}
