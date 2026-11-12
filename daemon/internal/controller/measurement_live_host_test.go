//go:build hostmeasurementlive

package controller

import (
	"context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
	"net"
	"os"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"testing"
	"time"
)

// Explicit live smoke reads only product values. It restores the pre-QA first
// local selection while OFF, probes one route, never enables a TUN or logs credentials.
func TestMeasurementLiveSmoke(t *testing.T) {
	socket := os.Getenv("PAX_V2_LIVE_SOCKET")
	if socket == "" {
		t.Skip("explicit live socket required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := grpc.NewClient("passthrough:///private", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}))
	if err != nil {
		t.Fatal("live client unavailable")
	}
	defer conn.Close()
	client := paxv1.NewPaxServiceClient(conn)
	snapshot, err := client.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if err != nil || snapshot.Status != paxv1.ProtectionStatus_PROTECTION_STATUS_OFF {
		t.Fatal("live snapshot must be OFF")
	}
	if len(snapshot.Routes) < 3 {
		t.Fatal("catalog missing")
	}
	original := snapshot.Routes[0]
	if _, err := client.SelectRoute(ctx, &paxv1.SelectRouteRequest{RouteId: original.Id, ExpectedRouteRevision: original.Revision}); err != nil {
		t.Fatal("restore selected route failed")
	}
	for i := 0; i < 100; i++ {
		snapshot, err = client.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.PendingOperationId == "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if snapshot.SelectedRouteId != original.Id {
		t.Fatal("selection restore did not commit")
	}
	prefs, err := client.GetMeasurementPreferences(ctx, &emptypb.Empty{})
	if err != nil || prefs.DownloadParallel != 8 {
		t.Fatal("default eight unavailable")
	}
	catalog, err := client.QueryCFCandidates(ctx, &emptypb.Empty{})
	if err != nil || len(catalog.Items) == 0 {
		t.Fatal("real candidate catalog unavailable")
	}
	t.Logf("PASS OFF catalog=%d seeds=%d defaults=%d/%d/%d", len(snapshot.Routes), len(catalog.Items), prefs.LatencyParallel, prefs.ExitParallel, prefs.DownloadParallel)
	ack, err := client.TestRoute(ctx, &paxv1.TestRouteRequest{RouteId: original.Id, ExpectedRouteRevision: original.Revision})
	if err != nil {
		t.Fatal("dual test unavailable")
	}
	_ = ack
	for i := 0; i < 170; i++ {
		snapshot, err = client.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
		if err != nil {
			t.Fatal(err)
		}
		var summary *paxv1.RouteTestSummary
		for _, node := range snapshot.Routes {
			if node.Id == original.Id {
				summary = node.TestSummary
			}
		}
		if summary != nil && summary.Method == "https204-median-v3" && len(snapshot.TestingRouteIds) == 0 {
			t.Logf("PASS session success=%t cold_ms=%d stable_ms=%d samples=%d code=%s", summary.Success, summary.ColdMs, summary.StableMs, summary.StableSamples, summary.Error.GetCode())
			if snapshot.Status != paxv1.ProtectionStatus_PROTECTION_STATUS_OFF || snapshot.SelectedRouteId != original.Id {
				t.Fatal("probe changed proxy or selection")
			}
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	egress, err := client.GetPhysicalEgress(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("physical phase=%s has_ipv4=%t has_ipv6=%t", egress.Phase, egress.Ipv4 != "", egress.Ipv6 != "")
	if os.Getenv("PAX_V2_VALIDATE_CF") == "1" {
		for _, node := range snapshot.Routes {
			if !node.CfEntry {
				continue
			}
			editor, e := client.GetRouteEditor(ctx, &paxv1.GetRouteEditorRequest{RouteId: node.Id})
			if e != nil {
				t.Fatal("CF editor unavailable")
			}
			value, e := client.ValidateCFCandidate(ctx, &paxv1.CFValidationRequest{RouteId: node.Id, ExpectedRevision: node.Revision, DocumentJson: editor.DocumentJson, Ip: node.Server})
			if e != nil {
				t.Fatal("CF validation RPC failed")
			}
			t.Logf("CF validation success=%t latency_ms=%d code=%s", value.Error == nil, value.LatencyMs, value.Error.GetCode())
			break
		}
	}
}
