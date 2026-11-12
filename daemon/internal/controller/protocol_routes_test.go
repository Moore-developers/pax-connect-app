package controller

import (
	"context"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"testing"
	"time"
)

func TestTrojanAndShadowsocksImportEditAndOfflineProbe(t *testing.T) {
	for _, link := range []string{
		"trojan://fixture-password@trojan.example.test:443?sni=trojan.example.test#Trojan",
		"ss://YWVzLTEyOC1nY206Zml4dHVyZS1wYXNzd29yZA@ss.example.test:8388#Shadowsocks",
	} {
		t.Run(link[:2], func(t *testing.T) {
			c, _ := newSubscriptionController(t, &fakeFetcher{})
			c.ConfigureRouteTester(&fakeTester{duration: 125 * time.Millisecond}, maxParallelTests)
			ctx := context.Background()
			results, stop := operationSink(c)
			defer stop()
			preview, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: link})
			if err != nil {
				t.Fatal(err)
			}
			if len(preview.Items) != 1 || preview.Items[0].Route == nil || preview.Items[0].ErrorCode != "" {
				t.Fatal("protocol preview rejected", preview)
			}
			ack, err := c.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, ItemIds: []string{preview.Items[0].ItemId}})
			if err != nil {
				t.Fatal(err)
			}
			if !awaitResult(t, results, ack.OperationId).Ok {
				t.Fatal("import failed")
			}
			// Import schedules its own offline probe. This test exercises a later
			// edit and explicit probe, so wait for import-time work before changing it.
			waitPoolCondition(t, func() bool {
				snapshot, _ := c.GetSnapshot(ctx, nil)
				return len(snapshot.TestingRouteIds) == 0 && len(snapshot.Routes) == 1 && snapshot.Routes[0].TestSummary != nil
			})
			node := waitComplete(t, c).Routes[0]
			editor, err := c.GetRouteEditor(ctx, &paxv1.GetRouteEditorRequest{RouteId: node.Id})
			if err != nil {
				t.Fatal(err)
			}
			ack, err = c.UpdateRoute(ctx, &paxv1.RouteEditRequest{RouteId: node.Id, ExpectedRevision: node.Revision, Name: "Renamed", DocumentJson: editor.DocumentJson})
			if err != nil {
				t.Fatal(err)
			}
			if !awaitResult(t, results, ack.OperationId).Ok {
				t.Fatal("document edit failed")
			}
			node = waitComplete(t, c).Routes[0]
			ack, err = c.TestRoute(ctx, &paxv1.TestRouteRequest{RouteId: node.Id, ExpectedRouteRevision: node.Revision})
			if err != nil {
				t.Fatal(err)
			}
			if result := awaitResult(t, results, ack.OperationId); !result.Ok {
				t.Fatal("offline probe failed", result)
			}
			snapshot := waitComplete(t, c)
			if snapshot.Routes[0].TestSummary.DurationMs != 125 || snapshot.Status != paxv1.ProtectionStatus_PROTECTION_STATUS_OFF {
				t.Fatal("probe result or state wrong")
			}
		})
	}
}

func TestInvalidShadowsocksMethodAndTrojanSecurityAreRejected(t *testing.T) {
	c, _ := newSubscriptionController(t, &fakeFetcher{})
	ctx := context.Background()
	for _, link := range []string{
		"ss://YWVzLTI1Ni1jZmI6cGFzcw@ss.example.test:8388#StreamCipher",
		"ss://YWVzLTEyOC1nY206cGFzcw@ss.example.test:8388?plugin=external#Plugin",
		"trojan://password@trojan.example.test:443?security=none#InsecureTransport",
	} {
		p, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: link})
		if err != nil {
			continue
		}
		if p.Items[0].ErrorCode == "" {
			t.Fatal("invalid shape was admitted")
		}
	}
	if len(waitComplete(t, c).Routes) != 0 {
		t.Fatal("invalid input persisted")
	}
}
