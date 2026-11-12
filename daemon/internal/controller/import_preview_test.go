package controller

import (
	"context"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"strings"
	"testing"
)

func TestImportOnlySelectedAndEmptySelectionDoesNotCreateSource(t *testing.T) {
	fetcher := &fakeFetcher{}
	c, _ := newSubscriptionController(t, fetcher)
	fetcher.set(subscriptionBody(vlessLink("One", "one.example.test", uuidA, "ws"), vlessLink("Two", "two.example.test", uuidB, "ws")), nil)
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	preview, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: subURL, IsSubscription: true})
	if err != nil {
		t.Fatal(err)
	}
	ack, err := c.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision})
	if err != nil {
		t.Fatal(err)
	}
	if awaitResult(t, results, ack.OperationId).Ok {
		t.Fatal("empty selection succeeded")
	}
	snapshot := waitComplete(t, c)
	if len(snapshot.Routes) != 0 || len(snapshot.Subscriptions) != 0 {
		t.Fatal("empty selection left records")
	}
	ack, err = c.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, ItemIds: []string{preview.Items[0].ItemId}})
	if err != nil {
		t.Fatal(err)
	}
	if !awaitResult(t, results, ack.OperationId).Ok {
		t.Fatal("selected import failed")
	}
	snapshot = waitComplete(t, c)
	if len(snapshot.Routes) != 1 || len(snapshot.Subscriptions) != 1 {
		t.Fatal("unselected route persisted")
	}
}

func TestPreviewBelongsToClientAndAllowsFourPending(t *testing.T) {
	c, _ := newSubscriptionController(t, &fakeFetcher{})
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	var preview *paxv1.RoutesPreview
	for i := 0; i < 4; i++ {
		var err error
		preview, err = c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: vlessLink("One", "one.example.test", uuidA, "ws"), ClientId: "client-a"})
		if err != nil {
			t.Fatal("four bounded previews must be allowed", err)
		}
	}
	if _, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: vlessLink("One", "one.example.test", uuidA, "ws"), ClientId: "client-a"}); err == nil {
		t.Fatal("fifth preview admitted")
	}
	if _, err := c.CancelRoutesPreview(ctx, &paxv1.CancelRoutesPreviewRequest{Token: preview.Token, ClientId: "client-b"}); err == nil {
		t.Fatal("foreign client cancelled preview")
	}
	ack, err := c.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ClientId: "client-b", ExpectedCatalogRevision: preview.CatalogRevision, ItemIds: []string{preview.Items[0].ItemId}})
	if err != nil {
		t.Fatal(err)
	}
	if awaitResult(t, results, ack.OperationId).Ok {
		t.Fatal("foreign client consumed preview")
	}
	ack, err = c.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ClientId: "client-a", ExpectedCatalogRevision: preview.CatalogRevision, ItemIds: []string{preview.Items[0].ItemId}})
	if err != nil {
		t.Fatal(err)
	}
	if !awaitResult(t, results, ack.OperationId).Ok {
		t.Fatal("owner could not confirm preview")
	}
}

func TestReimportUsesSourceDocumentAfterEditAndHide(t *testing.T) {
	fetcher := &fakeFetcher{}
	c, _ := newSubscriptionController(t, fetcher)
	fetcher.set(vlessLink("Source", "source.example.test", uuidA, "ws"), nil)
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	preview, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: subURL, IsSubscription: true})
	if err != nil {
		t.Fatal(err)
	}
	ack, err := c.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, ItemIds: []string{preview.Items[0].ItemId}})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	node := waitComplete(t, c).Routes[0]
	editor, err := c.GetRouteEditor(ctx, &paxv1.GetRouteEditorRequest{RouteId: node.Id})
	if err != nil {
		t.Fatal(err)
	}
	ack, err = c.UpdateRoute(ctx, &paxv1.RouteEditRequest{RouteId: node.Id, ExpectedRevision: node.Revision, DocumentJson: strings.Replace(editor.DocumentJson, "source.example.test", "edited.example.test", 1)})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	node = waitComplete(t, c).Routes[0]
	ack, err = c.DeleteRoute(ctx, &paxv1.DeleteRouteRequest{RouteId: node.Id, ExpectedRevision: node.Revision})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	preview, err = c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: subURL, IsSubscription: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 1 || !preview.Items[0].Duplicate || !preview.Items[0].Hidden {
		t.Fatal("source document lost duplicate/hidden identity")
	}
}

func TestConfirmImportRequestReplaysFinalResult(t *testing.T) {
	c, _ := newSubscriptionController(t, &fakeFetcher{})
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	preview, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: vlessLink("One", "one.example.test", uuidA, "ws"), ClientId: "app"})
	if err != nil {
		t.Fatal(err)
	}
	req := &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, ItemIds: []string{preview.Items[0].ItemId}, ClientId: "app", RequestId: "confirm-once"}
	first, err := c.ConfirmImportRoutes(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !awaitResult(t, results, first.OperationId).Ok {
		t.Fatal("first import failed")
	}
	second, err := c.ConfirmImportRoutes(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if second.OperationId != first.OperationId {
		t.Fatal("confirm retry created a new operation")
	}
	if !awaitResult(t, results, second.OperationId).Ok {
		t.Fatal("confirm retry lost result")
	}
	if len(waitComplete(t, c).Routes) != 1 {
		t.Fatal("confirm duplicated records")
	}
}
