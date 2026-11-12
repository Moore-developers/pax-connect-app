package controller

import (
	"context"
	"encoding/json"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/subfetch"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSubscriptionProxyFetchUsesFrozenRouteWithoutDirectFallback(t *testing.T) {
	direct := &fakeFetcher{}
	direct.set(vlessLink("Original", "node.test", uuidA, "ws"), nil)
	c, _ := newSubscriptionController(t, direct)
	results, stop := operationSink(c)
	defer stop()
	preview, err := c.PreviewRoutes(context.Background(), &paxv1.PreviewRoutesRequest{Source: subURL, IsSubscription: true})
	if err != nil {
		t.Fatal(err)
	}
	ack, err := c.ConfirmImportRoutes(context.Background(), &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, ItemIds: []string{preview.Items[0].ItemId}})
	if err != nil {
		t.Fatal(err)
	}
	if !awaitResult(t, results, ack.OperationId).Ok {
		t.Fatal("import rejected")
	}
	before := waitComplete(t, c)
	source := before.Subscriptions[0]
	route := before.Routes[0]
	var mu sync.Mutex
	var captured config.RouteConfig
	entered := make(chan struct{})
	release := make(chan struct{})
	c.ConfigureSubscriptionRouteFetch(func(ctx context.Context, p config.RouteConfig, _ string) ([]byte, error) {
		mu.Lock()
		captured = p
		mu.Unlock()
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return nil, subfetch.ErrForbidden
	})
	req := &paxv1.RefreshSubscriptionRequest{SubscriptionId: source.Id, ExpectedSourceRevision: source.Revision, RouteId: route.Id, ExpectedRouteRevision: route.Revision, ExpectedConfigRevision: route.ConfigRevision}
	ack, err = c.RefreshSubscription(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	duplicate, err := c.RefreshSubscription(context.Background(), req)
	if err != nil || duplicate.OperationId != ack.OperationId {
		t.Fatal("duplicate refresh not reused")
	}
	editor, err := c.GetRouteEditor(context.Background(), &paxv1.GetRouteEditorRequest{RouteId: route.Id})
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err = json.Unmarshal([]byte(editor.DocumentJson), &document); err != nil {
		t.Fatal(err)
	}
	document["server"] = "changed.test"
	changed, _ := json.Marshal(document)
	edit, err := c.UpdateRoute(context.Background(), &paxv1.RouteEditRequest{RouteId: route.Id, ExpectedRevision: route.Revision, DocumentJson: string(changed)})
	if err != nil {
		t.Fatal(err)
	}
	if !awaitResult(t, results, edit.OperationId).Ok {
		t.Fatal("edit rejected")
	}
	close(release)
	if result := awaitResult(t, results, ack.OperationId); result.Error.GetCode() != "fetch_forbidden" {
		t.Fatal(result)
	}
	mu.Lock()
	server := captured.Server
	mu.Unlock()
	if server != route.Server {
		t.Fatal("frozen route changed")
	}
	direct.mu.Lock()
	calls := direct.calls
	direct.mu.Unlock()
	if calls != 1 {
		t.Fatal("proxy failure fell back to direct", calls)
	}
	after := waitComplete(t, c)
	if after.Status != paxv1.ProtectionStatus_PROTECTION_STATUS_OFF || after.SelectedRouteId != before.SelectedRouteId {
		t.Fatal("refresh changed proxy/selection")
	}
	if len(after.Routes) != 1 || after.Subscriptions[0].LastError.GetCode() != "fetch_forbidden" {
		t.Fatal("failed refresh lost previous source")
	}
	for _, log := range after.RecentLogs {
		if strings.Contains(log.Message, "SUPERSECRET") {
			t.Fatal("private URL leaked")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.CloseProxy(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestMissingCoreRequiresAuthorizationBeforeRuntimeStart(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	node := seedLocalRoute(t, c)
	selectResults, stopSelect := operationSink(c)
	selected, err := c.SelectRoute(context.Background(), &paxv1.SelectRouteRequest{RouteId: node.Id, ExpectedRouteRevision: node.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if !awaitResult(t, selectResults, selected.OperationId).Ok {
		t.Fatal("selection rejected")
	}
	stopSelect()
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.SetProxyEnabled(context.Background(), &paxv1.SetProxyEnabledRequest{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); result.Error.GetCode() != "core_required" {
		t.Fatal(result)
	}
	if waitComplete(t, c).Status != paxv1.ProtectionStatus_PROTECTION_STATUS_OFF {
		t.Fatal("privilege guard not OFF")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.CloseProxy(ctx); err != nil {
		t.Fatal(err)
	}
}
