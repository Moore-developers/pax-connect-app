package controller

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"strings"
	"testing"
)

func TestSafeRouteEditCASAndCredentialUpdates(t *testing.T) {
	c, _ := newSubscriptionController(t, &fakeFetcher{})
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	raw := `{"type":"hysteria2","server":"edge.test","server_port":443,"password":"PRIVATE-SECRET","tls":{"enabled":true,"server_name":"edge.test"},"connect_timeout":"7s"}`
	ack, err := c.AddRoute(ctx, &pb.RouteEditRequest{Name: "Before", DocumentJson: raw})
	if err != nil {
		t.Fatal(err)
	}
	if !awaitResult(t, results, ack.OperationId).Ok {
		t.Fatal("seed failed")
	}
	node := waitComplete(t, c).Routes[0]
	view, err := c.GetRouteEditView(ctx, &pb.GetRouteEditorRequest{RouteId: node.Id})
	if err != nil || strings.Contains(view.String(), "PRIVATE-SECRET") {
		t.Fatalf("read leaked: %v %v", view, err)
	}
	req := &pb.SaveRouteEditRequest{RouteId: node.Id, ExpectedRevision: view.Revision, Name: "After", DocumentJson: view.DocumentJson, Context: operationIdentity(c)}
	ack, err = c.SaveRouteEdit(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	result := awaitResult(t, results, ack.OperationId)
	if !result.Ok || result.GetRouteSave().RouteRevision <= view.Revision || result.GetRouteSave().CatalogRevision == 0 {
		t.Fatalf("save failed: %v", result)
	}
	editor, _ := c.GetRouteEditor(ctx, &pb.GetRouteEditorRequest{RouteId: node.Id})
	if !strings.Contains(editor.DocumentJson, "PRIVATE-SECRET") {
		t.Fatal("credential lost")
	}
	// Same identity survives an App edit and preserves the original result.
	replay, err := c.SaveRouteEdit(ctx, req)
	if err != nil || replay.OperationId != ack.OperationId {
		t.Fatalf("replay: %v %v", replay, err)
	}
	req.Context = operationIdentity(c)
	req.Context.ClientId = "second-intent"
	req.Name = "Stale"
	ack, err = c.SaveRouteEdit(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	result = awaitResult(t, results, ack.OperationId)
	if result.Ok || result.Error.Code != "route_stale" || len(result.Error.MessageParams) != 1 {
		t.Fatalf("stale wrote: %v", result)
	}
	view, _ = c.GetRouteEditView(ctx, &pb.GetRouteEditorRequest{RouteId: node.Id})
	req = &pb.SaveRouteEditRequest{RouteId: node.Id, ExpectedRevision: view.Revision, Name: "Clear", DocumentJson: view.DocumentJson, SecretUpdates: []*pb.RouteSecretUpdate{{Path: "/password", Change: &pb.RouteSecretUpdate_Clear{Clear: true}}}}
	ack, err = c.SaveRouteEdit(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	result = awaitResult(t, results, ack.OperationId)
	if result.Ok || result.Error.Code != "credentials_required" {
		t.Fatalf("clear succeeded: %v", result)
	}
	req.SecretUpdates = []*pb.RouteSecretUpdate{{Path: "/password", Change: &pb.RouteSecretUpdate_Value{Value: "REPLACEMENT"}}}
	ack, err = c.SaveRouteEdit(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !awaitResult(t, results, ack.OperationId).Ok {
		t.Fatal("replace failed")
	}
	editor, _ = c.GetRouteEditor(ctx, &pb.GetRouteEditorRequest{RouteId: node.Id})
	if strings.Contains(editor.DocumentJson, "PRIVATE-SECRET") || !strings.Contains(editor.DocumentJson, "REPLACEMENT") {
		t.Fatal("replacement did not persist")
	}
}
func TestSafeRouteCreateCannotBypassTLS(t *testing.T) {
	c, _ := newSubscriptionController(t, &fakeFetcher{})
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	for _, raw := range []string{
		`{"type":"hysteria2","server":"edge.test","server_port":443,"password":"INLINE","tls":{"enabled":true,"server_name":"edge.test"}}`,
		`{"type":"hysteria2","server":"edge.test","server_port":443,"tls":{"enabled":true,"server_name":"edge.test","insecure":true}}`,
	} {
		ack, err := c.SaveRouteEdit(ctx, &pb.SaveRouteEditRequest{Name: "Denied", DocumentJson: raw, SecretUpdates: []*pb.RouteSecretUpdate{{Path: "/password", Change: &pb.RouteSecretUpdate_Value{Value: "secret"}}}})
		if err != nil {
			t.Fatal(err)
		}
		result := awaitResult(t, results, ack.OperationId)
		if result.Ok || len(waitComplete(t, c).Routes) != 0 {
			t.Fatalf("bad save wrote: %v", result)
		}
	}
}

func TestUnchangedRefreshRetainsRecoverableSourceResult(t *testing.T) {
	fetcher := &fakeFetcher{body: []byte("trojan://fixture@source.test:443#Source")}
	c, _ := newSubscriptionController(t, fetcher)
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	preview, err := c.PreviewRoutes(ctx, &pb.PreviewRoutesRequest{Source: subURL, IsSubscription: true})
	if err != nil {
		t.Fatal(err)
	}
	ack, err := c.ConfirmImportRoutes(ctx, &pb.ConfirmImportRoutesRequest{Token: preview.Token, ItemIds: []string{preview.Items[0].ItemId}, ExpectedCatalogRevision: preview.CatalogRevision})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	source := waitComplete(t, c).Subscriptions[0]
	identity := operationIdentity(c)
	ack, err = c.RefreshSubscription(ctx, &pb.RefreshSubscriptionRequest{SubscriptionId: source.Id, ExpectedSourceRevision: source.Revision, Context: identity})
	if err != nil {
		t.Fatal(err)
	}
	result := awaitResult(t, results, ack.OperationId)
	if !result.Ok || result.GetRouteSave().SubscriptionId != source.Id || result.GetRouteSave().SourceRevision != source.Revision || result.GetRouteSave().Updated != 1 {
		t.Fatalf("unchanged source lost commit facts: %v", result)
	}
	state, err := c.GetOperation(ctx, &pb.GetOperationRequest{DaemonInstance: c.instance, Identity: &pb.GetOperationRequest_OperationId{OperationId: ack.OperationId}})
	if err != nil || state.GetRouteSave().SubscriptionId != source.Id {
		t.Fatalf("refresh not recoverable: %v %v", state, err)
	}
}

func TestDocumentTLSChangeFollowsExplicitPolicyButCannotTransferConsent(t *testing.T) {
	c, _ := newSubscriptionController(t, &fakeFetcher{})
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := c.query(ctx, func(context.Context) {
		c.ConfigureTLS(config.NewTLSPolicyStore(filepath.Join(directory, "tls.json"), uint32(os.Getuid())))
	}); err != nil {
		t.Fatal(err)
	}
	ack, err := c.SaveRouteEdit(ctx, &pb.SaveRouteEditRequest{Name: "TLS", DocumentJson: `{"type":"http","server":"edge.test","server_port":443,"tls":{"enabled":true,"server_name":"edge.test"}}`})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	node := waitComplete(t, c).Routes[0]
	ack, err = c.UpdateRouteTLS(ctx, &pb.UpdateRouteTLSRequest{RouteId: node.Id, IdentityToken: node.TlsPolicy.IdentityToken, ExpectedRevision: node.TlsPolicy.PolicyRevision, Mode: pb.TLSMode_TLS_MODE_SKIP_VERIFY, ConfirmSkip: true})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	view, _ := c.GetRouteEditView(ctx, &pb.GetRouteEditorRequest{RouteId: node.Id})
	var document map[string]any
	if json.Unmarshal([]byte(view.DocumentJson), &document) != nil {
		t.Fatal("document")
	}
	document["tls"].(map[string]any)["insecure"] = true
	raw, _ := json.Marshal(document)
	ack, err = c.SaveRouteEdit(ctx, &pb.SaveRouteEditRequest{RouteId: node.Id, ExpectedRevision: view.Revision, Name: "TLS", DocumentJson: string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); !result.Ok {
		t.Fatal(result)
	}
	view, _ = c.GetRouteEditView(ctx, &pb.GetRouteEditorRequest{RouteId: node.Id})
	document["server"] = "other.test"
	raw, _ = json.Marshal(document)
	ack, err = c.SaveRouteEdit(ctx, &pb.SaveRouteEditRequest{RouteId: node.Id, ExpectedRevision: view.Revision, Name: "TLS", DocumentJson: string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); result.Ok || result.Error.Code != "tls_confirmation_required" {
		t.Fatal(result)
	}
}
