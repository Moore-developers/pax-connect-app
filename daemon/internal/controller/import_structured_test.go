package controller

import (
	"context"
	"strings"
	"testing"

	pb "pax-connect/daemon/gen/proto/paxv1"
)

func TestStructuredRefreshKeepsRejectedNodesAndLocalEdits(t *testing.T) {
	const front = `{"type":"shadowsocks","tag":"Front","server":"front.example.test","server_port":8388,"method":"aes-128-gcm","password":"fixture"}`
	const other = `{"type":"anytls","tag":"Other","server":"other.example.test","server_port":443,"password":"fixture","tls":{"enabled":true,"server_name":"other.example.test"}}`
	ctx := context.Background()
	fetcher := &fakeFetcher{}
	c, _ := newSubscriptionController(t, fetcher)
	results, stop := operationSink(c)
	defer stop()
	previewSource := func() *pb.RoutesPreview {
		t.Helper()
		preview, err := c.PreviewRoutes(ctx, &pb.PreviewRoutesRequest{Source: subURL, IsSubscription: true})
		if err != nil {
			t.Fatal(err)
		}
		return preview
	}
	fetcher.set(`{"outbounds":[`+front+`,`+other+`,{"type":"direct","tag":"direct"}]}`, nil)
	preview := previewSource()
	if len(preview.Items) != 2 || preview.Items[0].Route == nil || preview.Items[1].Route == nil {
		t.Fatal("expected two routable nodes, without direct")
	}
	ack, err := c.ConfirmImportRoutes(ctx, &pb.ConfirmImportRoutesRequest{
		Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, Automatic: true,
		ItemIds: []string{preview.Items[0].ItemId, preview.Items[1].ItemId},
	})
	if err != nil || !awaitResult(t, results, ack.OperationId).Ok {
		t.Fatal("initial structured import failed", err)
	}
	snapshot := waitComplete(t, c)
	if len(snapshot.Subscriptions) != 1 || len(snapshot.Routes) != 2 {
		t.Fatal("import did not save two routes and one source")
	}
	source := snapshot.Subscriptions[0]
	first := snapshot.Routes[0]
	editor, err := c.GetRouteEditor(ctx, &pb.GetRouteEditorRequest{RouteId: first.Id})
	if err != nil {
		t.Fatal(err)
	}
	ack, err = c.UpdateRoute(ctx, &pb.RouteEditRequest{RouteId: first.Id, ExpectedRevision: first.Revision, Name: "Local name", DocumentJson: editor.DocumentJson})
	if err != nil || !awaitResult(t, results, ack.OperationId).Ok {
		t.Fatal("local edit failed", err)
	}

	// client_metadata decodes as AnyTLS, but the fixed core disallows it.
	restricted := strings.TrimSuffix(front, "}") + `,"network":"tcp"}`
	rejected := strings.TrimSuffix(other, "}") + `,"client_metadata":"fixture-metadata"}`
	fetcher.set(`{"outbounds":[`+restricted+`,`+rejected+`]}`, nil)
	preview = previewSource()
	if len(preview.Items) != 2 || preview.Items[0].Route == nil || preview.Items[1].Route != nil || preview.Items[1].ErrorCode != "unsupported_field" {
		t.Fatal("preview failed to reject a disallowed outbound field")
	}
	ack, err = c.RefreshSubscription(ctx, &pb.RefreshSubscriptionRequest{SubscriptionId: source.Id, ExpectedSourceRevision: source.Revision})
	if err != nil || !awaitResult(t, results, ack.OperationId).Ok {
		t.Fatal("partial structured refresh failed", err)
	}
	snapshot = waitComplete(t, c)
	if len(snapshot.Routes) != 2 || snapshot.Routes[0].Id != first.Id || snapshot.Routes[0].Name != "Local name" || snapshot.Routes[1].SourceRemoved {
		t.Fatal("refresh lost the local edit or unmatched route")
	}
	if report := snapshot.Subscriptions[0].LastRefresh; !report.GetPartial() || report.GetKept() != 1 {
		t.Fatal("refresh did not report the partial merge")
	}
	editor, err = c.GetRouteEditor(ctx, &pb.GetRouteEditorRequest{RouteId: first.Id})
	if err != nil || !strings.Contains(editor.DocumentJson, `"network":"tcp"`) {
		t.Fatal("refresh did not retain the network constraint on readback", err)
	}
}
