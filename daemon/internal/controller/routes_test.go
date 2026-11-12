package controller

import (
	"context"
	"google.golang.org/grpc/status"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/subfetch"
	"slices"
	"testing"
	"time"
)

type memoryCatalog struct {
	catalog config.Catalog
	exists  bool
}

func (m *memoryCatalog) Load() (config.Catalog, bool, error) { return m.catalog.Clone(), m.exists, nil }
func (m *memoryCatalog) Save(_ context.Context, c config.Catalog) error {
	m.catalog = c.Clone()
	m.exists = true
	return nil
}
func TestPreviewCancelConfirmAndOfflineSelection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := New("test", "")
	store := &memoryCatalog{catalog: config.NewCatalog(), exists: true}
	c.ConfigureCatalog(store)
	c.ConfigureOperations(ctx, func() (config.DebugProfile, error) { return config.DebugProfile{}, config.ErrProfileUnavailable })
	preview, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: "hysteria2://secret@example.com:443?sni=example.com#Test\nunknown://bad"})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 2 || preview.Items[1].ErrorCode == "" {
		t.Fatal("preview missing partial error")
	}
	snapshot, _ := c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if len(snapshot.Routes) != 0 {
		t.Fatal("preview wrote")
	}
	if _, err := c.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, ItemIds: []string{preview.Items[0].ItemId}}); err != nil {
		t.Fatal(err)
	}
	waitComplete(t, c)
	time.Sleep(2100 * time.Millisecond)
	snapshot, _ = c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if len(snapshot.Routes) != 1 {
		t.Fatal("confirm did not persist")
	}
	route := snapshot.Routes[0]
	if _, err := c.SelectRoute(ctx, &paxv1.SelectRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision}); err != nil {
		t.Fatal(err)
	}
	snapshot = waitComplete(t, c)
	if snapshot.SelectedRouteId != route.Id || snapshot.Status != paxv1.ProtectionStatus_PROTECTION_STATUS_OFF {
		t.Fatal("OFF select started proxy")
	}
	if _, err := c.DeleteRoute(ctx, &paxv1.DeleteRouteRequest{RouteId: route.Id, ExpectedRevision: route.Revision}); err != nil {
		t.Fatal(err)
	}
	if len(waitComplete(t, c).Routes) != 0 {
		t.Fatal("delete failed")
	}
}

func TestCancelledPreviewNeverWritesAndStaleCommitRejected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := New("test", "")
	store := &memoryCatalog{catalog: config.NewCatalog(), exists: true}
	c.ConfigureCatalog(store)
	c.ConfigureOperations(ctx, func() (config.DebugProfile, error) { return config.DebugProfile{}, config.ErrProfileUnavailable })
	preview, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: "hysteria2://secret@example.com:443?sni=example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CancelRoutesPreview(ctx, &paxv1.CancelRoutesPreviewRequest{Token: preview.Token}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, ItemIds: []string{preview.Items[0].ItemId}}); err != nil {
		t.Fatal(err)
	}
	if len(waitComplete(t, c).Routes) != 0 {
		t.Fatal("cancelled preview wrote")
	}
}

func TestHelloAdvertisesOnlyAvailableRouteActions(t *testing.T) {
	c := New("test", "")
	ctx := context.Background()
	hello, err := c.Hello(ctx, &paxv1.HelloRequest{ProtocolVersion: ProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	if len(hello.Capabilities) != 1 || hello.Capabilities[0] != "routes.catalog" {
		t.Fatalf("unconfigured actions advertised: %v", hello.Capabilities)
	}
	c.ConfigureCatalog(&memoryCatalog{catalog: config.NewCatalog(), exists: true})
	lifetime, cancel := context.WithCancel(ctx)
	defer cancel()
	c.ConfigureOperations(lifetime, func() (config.DebugProfile, error) { return config.DebugProfile{}, config.ErrProfileUnavailable })
	hello, err = c.Hello(ctx, &paxv1.HelloRequest{ProtocolVersion: ProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	if len(hello.Capabilities) != 11 || !slices.Contains(hello.Capabilities, "routes.select.off") || !slices.Contains(hello.Capabilities, "routes.proxy.create") || !slices.Contains(hello.Capabilities, "proxy.power") || !slices.Contains(hello.Capabilities, "routing.rules.read.v1") {
		t.Fatalf("unexpected configured capabilities: %v", hello.Capabilities)
	}
	if _, err = c.Hello(ctx, &paxv1.HelloRequest{ProtocolVersion: 3}); err == nil {
		t.Fatal("old semantic protocol accepted")
	}
}

func TestImportSelectsFirstOnlyWhenNoExistingSelection(t *testing.T) {
	c, _ := newSubscriptionController(t, &fakeFetcher{})
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	for i := 0; i < 2; i++ {
		raw := "trojan://fixture@first.test:443#First"
		if i == 1 {
			raw = "trojan://fixture@second.test:443#Second"
		}
		preview, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: raw})
		if err != nil {
			t.Fatal(err)
		}
		before := waitComplete(t, c).SelectedRouteId
		ack, err := c.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, ItemIds: []string{preview.Items[0].ItemId}})
		if err != nil {
			t.Fatal(err)
		}
		if result := awaitResult(t, results, ack.OperationId); !result.Ok {
			t.Fatal(result)
		}
		after := waitComplete(t, c)
		if after.GetDegradedReason().GetCode() == "profile_invalid" {
			t.Fatal("successful import retained empty-catalog error")
		}
		if after.SelectedRouteId == "" {
			t.Fatal("import left initial selection empty")
		}
		if before != "" && after.SelectedRouteId != before {
			t.Fatal("import replaced existing selection")
		}
		if after.Status != paxv1.ProtectionStatus_PROTECTION_STATUS_OFF {
			t.Fatal("import started proxy")
		}
	}
}

func TestSubscriptionDeniedRemainsSpecificAtProductRPC(t *testing.T) {
	c, _ := newSubscriptionController(t, &fakeFetcher{err: subfetch.ErrForbidden})
	_, err := c.PreviewRoutes(context.Background(), &paxv1.PreviewRoutesRequest{Source: "https://example.test/sub", IsSubscription: true})
	if status.Convert(err).Message() != "fetch_forbidden" {
		t.Fatal("403 collapsed to generic fetch failure")
	}
}
