package controller

import (
	"context"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/subfetch"
	"strings"
	"testing"
)

func TestReplaceSubscriptionCommitsOnlyAfterSuccessfulFetch(t *testing.T) {
	f := &fakeFetcher{}
	f.set(vlessLink("Original", "node.test", uuidA, "ws"), nil)
	c, _ := newSubscriptionController(t, f)
	results, stop := operationSink(c)
	defer stop()
	preview, err := c.PreviewRoutes(context.Background(), &pb.PreviewRoutesRequest{Source: subURL, IsSubscription: true})
	if err != nil {
		t.Fatal(err)
	}
	ack, err := c.ConfirmImportRoutes(context.Background(), &pb.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, Automatic: true})
	if err != nil {
		t.Fatal(err)
	}
	if !awaitResult(t, results, ack.OperationId).Ok {
		t.Fatal("import")
	}
	before := waitComplete(t, c)
	source := before.Subscriptions[0]
	node := before.Routes[0]
	replacement := "https://replacement.example/sub?token=PRIVATE_NEW"
	f.set("", subfetch.ErrForbidden)
	ack, err = c.RefreshSubscription(context.Background(), &pb.RefreshSubscriptionRequest{SubscriptionId: source.Id, ExpectedSourceRevision: source.Revision, ReplacementUrl: replacement})
	if err != nil {
		t.Fatal(err)
	}
	if awaitResult(t, results, ack.OperationId).Ok {
		t.Fatal("403 accepted")
	}
	failed := waitComplete(t, c)
	if failed.Subscriptions[0].Host != "sub.example.com" || len(failed.Routes) != 1 {
		t.Fatal("failure overwrote source")
	}
	if failed.Subscriptions[0].LastAttempt == nil || failed.Subscriptions[0].LastAttempt.ErrorCode != "fetch_forbidden" {
		t.Fatal("missing timestamped refresh evidence")
	}
	f.set(vlessLink("Original", "node.test", uuidA, "ws"), nil)
	ack, err = c.RefreshSubscription(context.Background(), &pb.RefreshSubscriptionRequest{SubscriptionId: source.Id, ExpectedSourceRevision: source.Revision, ReplacementUrl: replacement})
	if err != nil {
		t.Fatal(err)
	}
	if !awaitResult(t, results, ack.OperationId).Ok {
		t.Fatal("replace")
	}
	after := waitComplete(t, c)
	if len(after.Subscriptions) != 1 || after.Subscriptions[0].Id != source.Id || after.Subscriptions[0].Host != "replacement.example" || after.Routes[0].Id != node.Id || after.Subscriptions[0].Revision <= source.Revision {
		t.Fatal("source not replaced in place")
	}
	if strings.Contains(after.String(), "PRIVATE_NEW") || strings.Contains(after.String(), "SUPERSECRET") {
		t.Fatal("secret leaked")
	}
}
