//go:build integration

package controller_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/controller"
	"pax-connect/daemon/internal/singbox"
	"pax-connect/daemon/internal/subfetch"
	"testing"
	"time"
)

// Explicit opt-in: anonymous official HTTPS downloads into a temporary local
// library, no user catalog, credentials, core, TUN or system proxy.
func TestGeoPublicOfficialSources(t *testing.T) {
	if os.Getenv("PAX_GEO_PUBLIC") != "1" {
		t.Skip("opt-in official public Geo download")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(dir, 0700)
	c := controller.New("geo-public-test", "")
	c.ConfigureCatalog(config.NewCatalogStore(filepath.Join(dir, "routes.json"), uint32(os.Geteuid())))
	c.ConfigureRoutingLibrary(config.NewRoutingLibraryStore(filepath.Join(dir, "routing.json"), uint32(os.Geteuid())), func(raw []byte) ([]config.RoutingEntry, string, error) {
		entries, version, err := singbox.DecodeRoutingSource(raw)
		t.Logf("official body bytes=%d header=%x groups=%d decode_error=%v", len(raw), raw[:min(4, len(raw))], len(entries), err)
		for _, entry := range entries {
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(entry.Match, &fields)
			for key, raw := range fields {
				var values []string
				if json.Unmarshal(raw, &values) == nil {
					t.Logf("predicate field=%s count=%d", key, len(values))
				} else {
					t.Logf("predicate field=%s scalar", key)
				}
			}
		}
		return entries, version, err
	})
	lifetime, cancel := context.WithCancel(context.Background())
	c.ConfigureOperations(lifetime, func() (config.DebugProfile, error) { return config.DebugProfile{}, config.ErrProfileUnavailable })
	c.ConfigureSubscriptions(subfetch.New())
	defer func() {
		cancel()
		ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		_ = c.CloseProxy(ctx)
	}()
	ctx, done := context.WithTimeout(context.Background(), 55*time.Second)
	defer done()
	value, err := c.MutateRoutingLibrary(ctx, &pb.RoutingMutationRequest{ExpectedRevision: 1, Action: "update_geo", PackageId: "geo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(value.Packages) != 2 || value.Packages[1].UpdatedUnix == 0 {
		t.Fatal("update did not commit metadata")
	}
	page, err := c.QueryGeoRules(ctx, &pb.GeoRulesQuery{Limit: 100})
	if err != nil || page.Total < 100 || len(page.Rules) != 100 || page.NextOffset != 100 {
		t.Fatal("official rules not browsable", err)
	}
	t.Logf("official update committed temporary revision=%d members=%d version=%s", value.Revision, page.Total, page.Version)
}
