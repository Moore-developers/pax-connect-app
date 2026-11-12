package config

import "testing"

func TestLoadDemoCatalogDebug(t *testing.T) {
	store := NewCatalogStore("/private/tmp/pax-dev/demo-routes.json", uint32(501))
	catalog, exists, err := store.Load()
	t.Logf("exists=%v err=%v routes=%d subs=%d", exists, err, len(catalog.Routes), len(catalog.Subscriptions))
}
