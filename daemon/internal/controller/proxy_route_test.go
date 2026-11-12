package controller

import (
	"context"
	"encoding/json"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"strings"
	"testing"
)

func TestPurchasedProxyAddResultAndRetry(t *testing.T) {
	c, _ := newSubscriptionController(t, &fakeFetcher{})
	results, stop := operationSink(c)
	defer stop()
	ctx := context.Background()
	for _, kind := range []string{"socks", "http", "https"} {
		typ, tls := kind, ""
		if kind == "https" {
			typ = "http"
			tls = `,"tls":{"enabled":true,"server_name":"proxy.example"}`
		}
		req := &paxv1.RouteEditRequest{ClientId: "proxy-test", RequestId: kind, DocumentJson: `{"type":"` + typ + `","tag":"` + kind + `","server":"proxy.example","server_port":1080,"username":"PRIVATE-PROXY-USER","password":"PRIVATE-PROXY-PASS"` + tls + `}`}
		ack, err := c.AddRoute(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		result := awaitResult(t, results, ack.OperationId)
		if !result.Ok || result.RouteSave == nil || result.RouteSave.RouteId == "" || !result.RouteSave.Created {
			t.Fatalf("missing outcome: %v", result)
		}
		retry, err := c.AddRoute(ctx, req)
		if err != nil || retry.OperationId != ack.OperationId {
			t.Fatalf("retry: %v", err)
		}
		replay := awaitResult(t, results, retry.OperationId)
		if replay.RouteSave.GetRouteId() != result.RouteSave.RouteId {
			t.Fatal("retry lost outcome")
		}
		req.RequestId += "-duplicate"
		ack, err = c.AddRoute(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		duplicate := awaitResult(t, results, ack.OperationId)
		if !duplicate.Ok || duplicate.RouteSave.GetCreated() || duplicate.RouteSave.GetRouteId() != result.RouteSave.RouteId {
			t.Fatal("duplicate did not identify existing route")
		}
		snapshot := waitComplete(t, c)
		if snapshot.CurrentRoute != nil || snapshot.SelectedRouteId != "" {
			t.Fatal("adding selected a proxy")
		}
		if strings.Contains(snapshot.String(), "PRIVATE-PROXY") {
			t.Fatal("credentials leaked into snapshot")
		}
		editor, err := c.GetRouteEditor(ctx, &paxv1.GetRouteEditorRequest{RouteId: result.RouteSave.RouteId})
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]any
		if err := json.Unmarshal([]byte(editor.DocumentJson), &object); err != nil {
			t.Fatal(err)
		}
		delete(object, "username")
		delete(object, "password")
		anonymousJSON, err := json.Marshal(object)
		if err != nil {
			t.Fatal(err)
		}
		anonymous := string(anonymousJSON)
		node := snapshot.Routes[len(snapshot.Routes)-1]
		for _, route := range snapshot.Routes {
			if route.Id == result.RouteSave.RouteId {
				node = route
			}
		}
		editAck, err := c.UpdateRoute(ctx, &paxv1.RouteEditRequest{RouteId: node.Id, ExpectedRevision: node.Revision, DocumentJson: anonymous})
		if err != nil {
			t.Fatal(err)
		}
		if edited := awaitResult(t, results, editAck.OperationId); !edited.Ok {
			t.Fatal(edited)
		}
	}
}
