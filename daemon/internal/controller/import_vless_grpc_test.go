package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	pb "pax-connect/daemon/gen/proto/paxv1"
)

func TestVLESSGRPCTLSRefreshKeepsIDsAndServiceNameOverrides(t *testing.T) {
	ctx := context.Background()
	fetcher := &fakeFetcher{}
	c, _ := newSubscriptionController(t, fetcher)
	results, stop := operationSink(c)
	defer stop()

	grpc := func(name, server, service string) string {
		return fmt.Sprintf(`{"type":"vless","tag":%q,"server":%q,"server_port":443,"uuid":%q,"tls":{"enabled":true,"alpn":"h2"},"transport":{"type":"grpc","service_name":%q}}`, name, server, uuidA, service)
	}
	body := func(nodes ...string) string { return `{"outbounds":[` + strings.Join(nodes, ",") + `]}` }
	follow := grpc("Provider", "first.example.test", "Original")
	overridden := grpc("Override", "second.example.test", "Original")
	old := grpc("Kept", "third.example.test", "Kept")
	fetcher.set(body(follow, overridden, old), nil)
	preview, err := c.PreviewRoutes(ctx, &pb.PreviewRoutesRequest{Source: subURL, IsSubscription: true})
	if err != nil || len(preview.GetItems()) != 3 {
		t.Fatalf("preview: %v, %v", preview, err)
	}
	var items []string
	for _, item := range preview.Items {
		if item.Route == nil {
			t.Fatalf("valid TLS route rejected: %s", item.ErrorCode)
		}
		items = append(items, item.ItemId)
	}
	confirm, err := c.ConfirmImportRoutes(ctx, &pb.ConfirmImportRoutesRequest{
		Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, ItemIds: items,
	})
	if err != nil || !awaitResult(t, results, confirm.OperationId).Ok {
		t.Fatal("import failed", err)
	}
	snapshot := waitComplete(t, c)
	if len(snapshot.Routes) != 3 || len(snapshot.Subscriptions) != 1 {
		t.Fatal("import did not save all routes in one subscription")
	}
	byName := make(map[string]*pb.NodeProfile)
	for _, route := range snapshot.Routes {
		byName[route.Name] = route
	}
	first, second, third := byName["Provider"], byName["Override"], byName["Kept"]
	if first == nil || second == nil || third == nil {
		t.Fatal("import did not keep source names")
	}

	view := func(id string) *pb.RouteEditView {
		t.Helper()
		v, err := c.GetRouteEditView(ctx, &pb.GetRouteEditorRequest{RouteId: id})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	serviceName := func(v *pb.RouteEditView) string {
		t.Helper()
		var doc struct {
			Transport struct {
				ServiceName string `json:"service_name"`
			} `json:"transport"`
		}
		if err := json.Unmarshal([]byte(v.DocumentJson), &doc); err != nil {
			t.Fatal(err)
		}
		return doc.Transport.ServiceName
	}
	editor, err := c.GetRouteEditor(ctx, &pb.GetRouteEditorRequest{RouteId: first.Id})
	if err != nil {
		t.Fatal(err)
	}
	editor.Fields.Name = "Local name"
	rename, err := c.UpdateRoute(ctx, editor.Fields)
	if err != nil || !awaitResult(t, results, rename.OperationId).Ok {
		t.Fatal("rename failed", err)
	}
	renamed := view(first.Id)
	if renamed.Name != "Local name" || serviceName(renamed) != "Original" || renamed.ConfigRevision != editor.ConfigRevision {
		t.Fatal("rename changed connection parameters")
	}
	editor, err = c.GetRouteEditor(ctx, &pb.GetRouteEditorRequest{RouteId: first.Id})
	if err != nil {
		t.Fatal(err)
	}
	editor.Fields.Fingerprint = "invalid-fixture-fingerprint"
	rejected, err := c.UpdateRoute(ctx, editor.Fields)
	if err != nil || awaitResult(t, results, rejected.OperationId).GetError().GetCode() != "unsupported_field" {
		t.Fatal("typed edit bypassed kernel validation", err)
	}
	if view(first.Id).Revision != renamed.Revision {
		t.Fatal("invalid edit changed the saved record")
	}

	// An explicitly empty ALPN list uses the kernel default and survives the
	// private App edit and redacted readback without introducing a null field.
	emptyALPN := strings.Replace(editor.DocumentJson, `"alpn":["h2"]`, `"alpn":[]`, 1)
	saved, err := c.UpdateRoute(ctx, &pb.RouteEditRequest{
		RouteId: first.Id, ExpectedRevision: renamed.Revision, Name: renamed.Name, DocumentJson: emptyALPN,
	})
	if err != nil || !awaitResult(t, results, saved.OperationId).Ok {
		t.Fatal("edit with default ALPN failed", err)
	}
	withDefaultALPN := view(first.Id)
	if strings.Contains(withDefaultALPN.DocumentJson, `"alpn"`) || serviceName(withDefaultALPN) != "Original" {
		t.Fatal("the default ALPN changed the editable document or the gRPC service")
	}

	previous := view(second.Id)
	override, err := c.SaveRouteEdit(ctx, &pb.SaveRouteEditRequest{
		RouteId: second.Id, ExpectedRevision: previous.Revision, Name: previous.Name,
		DocumentJson: strings.Replace(previous.DocumentJson, `"service_name":"Original"`, `"service_name":"Local/Case+"`, 1),
	})
	if err != nil || !awaitResult(t, results, override.OperationId).Ok {
		t.Fatal("service-name edit failed", err)
	}
	edited := view(second.Id)
	if serviceName(edited) != "Local/Case+" || edited.ConfigRevision == previous.ConfigRevision {
		t.Fatal("service-name edit was not reflected in the connection configuration")
	}

	rejectedSource := strings.Replace(old, `"type":"grpc"`, `"type":"grpc","mode":"multi"`, 1)
	fetcher.set(body(grpc("Provider", "first.example.test", "Updated"), grpc("Override", "second.example.test", "Updated"), rejectedSource), nil)
	source := snapshot.Subscriptions[0]
	refresh, err := c.RefreshSubscription(ctx, &pb.RefreshSubscriptionRequest{
		SubscriptionId: source.Id, ExpectedSourceRevision: source.Revision,
	})
	if err != nil || !awaitResult(t, results, refresh.OperationId).Ok {
		t.Fatal("partial refresh failed", err)
	}
	refreshed := waitComplete(t, c)
	if report := refreshed.Subscriptions[0].LastRefresh; !report.GetPartial() || report.GetKept() != 1 || report.GetUpdated() != 2 {
		t.Fatalf("unexpected refresh report: %v", report)
	}
	if len(refreshed.Routes) != 3 {
		t.Fatalf("refresh changed number of routes: %d", len(refreshed.Routes))
	}
	if v := view(first.Id); v.Name != "Local name" || serviceName(v) != "Updated" || v.ConfigRevision == withDefaultALPN.ConfigRevision {
		t.Fatal("untouched service name failed to follow its source on the same route")
	}
	if v := view(second.Id); serviceName(v) != "Local/Case+" || v.ConfigRevision != edited.ConfigRevision {
		t.Fatal("refresh changed the effective local override")
	}
	if v := view(third.Id); serviceName(v) != "Kept" || v.SourceRemoved {
		t.Fatal("partial refresh discarded the previously valid route")
	}
}
