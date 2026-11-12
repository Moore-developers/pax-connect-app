//go:build hostdiagnostic

package controller

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pb "pax-connect/daemon/gen/proto/paxv1"
)

// Explicit live-App RPC acceptance: read-only OS/core evidence and one small
// HTTPS request. No route names, credentials, addresses or raw errors are logged.
func TestLiveAppConnectionDiagnosis(t *testing.T) {
	socket := os.Getenv("PAX_CONNECTION_DIAGNOSTIC_SOCKET")
	if socket == "" {
		t.Skip("explicit live App acceptance only")
	}
	connection, err := grpc.NewClient("passthrough:///connection-diagnosis", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}))
	if err != nil {
		t.Fatal("IPC client unavailable")
	}
	defer func() { _ = connection.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := pb.NewPaxServiceClient(connection)
	before, err := client.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
	if err != nil {
		t.Fatal("snapshot unavailable")
	}
	report, err := client.DiagnoseConnection(ctx, &pb.ConnectionDiagnosticRequest{Recheck: true})
	if err != nil {
		t.Fatal("diagnostic RPC unavailable")
	}
	if len(report.Steps) != 6 || !report.Rechecked {
		t.Fatal("fixed report incomplete")
	}
	for _, step := range report.Steps {
		t.Logf("kind=%s state=%s code=%s duration_ms=%d", step.Kind, step.State, step.Code, step.DurationMs)
	}
	after, err := client.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
	if err != nil {
		t.Fatal("final snapshot unavailable")
	}
	if before.Status != after.Status || before.GetCurrentRoute().GetId() != after.GetCurrentRoute().GetId() {
		t.Fatal("runtime changed during read-only acceptance")
	}
	if report.ChangedDuringCapture {
		t.Fatal("capture invalidated; repeat in stable environment")
	}
	if report.Steps[3].State != "pass" || report.Steps[5].State != "pass" {
		t.Fatal("selected target did not complete this live node/HTTPS probe; controlled states above")
	}
}
