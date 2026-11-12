//go:build realacceptance

package controller

import (
	"context"
	"fmt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
	"net"
	"os"
	"path/filepath"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"testing"
	"time"
)

func lifecycleClient(t *testing.T) (paxv1.PaxServiceClient, context.Context, func()) {
	t.Helper()
	dir := os.Getenv("PAX_REAL_ACCEPTANCE_DIRECTORY")
	if dir != "/private/tmp/pax-real-qa-20261015" && dir != "/private/tmp/pax-empty-qa-20261015" {
		t.Skip("named isolated directory only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	conn, err := grpc.NewClient("passthrough:///qa", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, "p.sock"))
	}))
	if err != nil {
		t.Fatal(err)
	}
	return paxv1.NewPaxServiceClient(conn), ctx, func() { cancel(); _ = conn.Close() }
}
func TestRealBackgroundDownloadAfterClientAndUIExit(t *testing.T) {
	client, ctx, closeClient := lifecycleClient(t)
	defer closeClient()
	before, err := client.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	req := &paxv1.TestRouteRequest{Kind: "download"}
	for _, node := range before.Routes {
		if node.Name == "QA-AutoImport" {
			continue
		}
		req.Targets = append(req.Targets, &paxv1.RouteTestTarget{RouteId: node.Id, ExpectedRouteRevision: node.Revision})
		if len(req.Targets) == 3 {
			break
		}
	}
	if len(req.Targets) < 2 {
		t.Fatal("real target set unavailable")
	}
	admission, cancel := context.WithCancel(ctx)
	ack, err := client.TestRoute(admission, req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	if err := os.WriteFile("/private/tmp/pax-real-qa-20261015/ui-exit-test-ready", []byte(ack.OperationId), 0600); err != nil {
		t.Fatal(err)
	}
	stream, err := client.Watch(ctx, &paxv1.WatchRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for {
		event, e := stream.Recv()
		if e != nil {
			t.Fatal(e)
		}
		result := event.GetOperation()
		if result == nil || result.OperationId != ack.OperationId {
			continue
		}
		after, e := client.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
		if e != nil {
			t.Fatal(e)
		}
		summaries := 0
		for _, node := range after.Routes {
			if node.DownloadSummary != nil && node.DownloadSummary.Method == "window6s-v2" {
				summaries++
			}
		}
		if after.Status != paxv1.ProtectionStatus_PROTECTION_STATUS_OFF || after.SelectedRouteId != before.SelectedRouteId {
			t.Fatal("background test changed selection or power")
		}
		t.Logf("ACCEPT F6 PASS admission client cancelled, UI may exit; terminal=%s summaries=%d", result.Error.GetCode(), summaries)
		break
	}
}
func TestRealRestartPersistence(t *testing.T) {
	client, ctx, closeClient := lifecycleClient(t)
	defer closeClient()
	snapshot, err := client.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	prefs, err := client.GetMeasurementPreferences(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	dual, download := 0, 0
	for _, node := range snapshot.Routes {
		if node.TestSummary.GetMethod() == "https204-median-v3" {
			dual++
		}
		if node.DownloadSummary.GetMethod() == "window6s-v2" {
			download++
		}
	}
	if dual == 0 || download == 0 || len(snapshot.TestingRouteIds) != 0 || len(snapshot.DownloadingRouteIds) != 0 || snapshot.Status != paxv1.ProtectionStatus_PROTECTION_STATUS_OFF {
		t.Fatal("persistence or pending-state recovery failed")
	}
	t.Logf("ACCEPT F9 PASS restarted daemon dual=%d downloads=%d prefs=%d/%d/%d no pending replay", dual, download, prefs.LatencyParallel, prefs.ExitParallel, prefs.DownloadParallel)
}
func TestRealEmptyImportSelectsWithoutEnabling(t *testing.T) {
	client, ctx, closeClient := lifecycleClient(t)
	defer closeClient()
	before, err := client.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if err != nil || len(before.Routes) != 0 {
		t.Fatal("empty QA required")
	}
	stream, err := client.Watch(ctx, &paxv1.WatchRequest{})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := client.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: "trojan://qa-fixture@empty-qa.example.test:443?sni=empty-qa.example.test#QA-Empty", ClientId: "empty-qa"})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, item := range preview.Items {
		if item.ErrorCode == "" {
			ids = append(ids, item.ItemId)
		}
	}
	ack, err := client.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, ItemIds: ids, Automatic: true, ClientId: "empty-qa", RequestId: "empty-import"})
	if err != nil {
		t.Fatal(err)
	}
	for {
		event, e := stream.Recv()
		if e != nil {
			t.Fatal(e)
		}
		if result := event.GetOperation(); result != nil && result.OperationId == ack.OperationId {
			if !result.Ok {
				t.Fatal(result.Error.Code)
			}
			break
		}
	}
	after, err := client.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if err != nil || len(after.Routes) != 1 || after.SelectedRouteId == "" || after.Status != paxv1.ProtectionStatus_PROTECTION_STATUS_OFF {
		t.Fatal("initial selection invalid")
	}
	t.Log(fmt.Sprintf("ACCEPT B5 PASS initial selected=%t status=OFF", after.SelectedRouteId != ""))
}

// TestReadOnlyRepairEndpoint checks the development daemon without selecting,
// downloading, enabling the proxy, or exposing private route documents.
func TestReadOnlyRepairEndpoint(t *testing.T) {
	socket := os.Getenv("PAX_REPAIR_READ_ONLY_SOCKET")
	if socket != "/private/tmp/pax-network-acceptance/p.sock" {
		t.Skip("named development socket required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.NewClient("passthrough:///repair", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}))
	if err != nil {
		t.Fatal("read-only transport unavailable")
	}
	defer conn.Close()
	client := paxv1.NewPaxServiceClient(conn)
	state, err := client.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if err != nil {
		t.Fatal("snapshot unavailable")
	}
	if state.Status != paxv1.ProtectionStatus_PROTECTION_STATUS_OFF || state.PendingOperationId != "" || len(state.TestingRouteIds) > 0 || len(state.DownloadingRouteIds) > 0 {
		t.Fatal("development daemon not idle/OFF")
	}
	hello, err := client.Hello(ctx, &paxv1.HelloRequest{ProtocolVersion: ProtocolVersion})
	if err != nil {
		t.Fatal("hello unavailable")
	}
	queue := false
	for _, capability := range hello.Capabilities {
		if capability == "routes.download.queue" {
			queue = true
		}
	}
	if os.Getenv("PAX_REPAIR_EXPECT_QUEUE") == "1" && !queue {
		t.Fatal("queue capability missing")
	}
	t.Logf("READ_ONLY OFF routes=%d sources=%d idle=true queue=%t", len(state.Routes), len(state.Subscriptions), queue)
}
