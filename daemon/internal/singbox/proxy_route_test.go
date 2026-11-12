package singbox

import (
	"context"
	"fmt"
	"pax-connect/daemon/internal/config"
	"testing"
)

func TestPurchasedProxyProfiles(t *testing.T) {
	for _, kind := range []string{"socks", "http", "https"} {
		t.Run(kind, func(t *testing.T) {
			typ, tls := kind, ""
			if kind == "https" {
				typ = "http"
				tls = `,"tls":{"enabled":true,"server_name":"proxy.example"}`
			}
			document := fmt.Sprintf(`{"type":%q,"server":"127.0.0.1","server_port":1080,"username":"fixture","password":"secret"%s}`, typ, tls)
			p, err := config.RouteFromDocument([]byte(document), "proxy-fixture", "Proxy")
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateRouteDocument(context.Background(), p.Document); err != nil {
				t.Fatal(err)
			}
			raw, err := CoreProfile(context.Background(), p, testRules(t), "127.0.0.1:17893")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ValidateCoreOptions(context.Background(), []byte(raw), t.TempDir(), 17893); err != nil {
				t.Fatal(err)
			}
			// Existing legacy fixed-field builders must also preserve proxy credentials.
			p.Document = nil
			if _, err := routeOutbound(p, "proxy"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
