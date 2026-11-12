//go:build integration

package controller

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
	"pax-connect/daemon/internal/ipc"
)

// Exercise actual Unix gRPC serialization and token continuation using private fixture data.
func TestLogRetentionUnixRPC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, p := newLogsTestController(t, newLogsTestDir(t))
	now := time.Now()
	p.emit(config.HistoryEvent{ID: "old", At: now.Add(-2 * time.Hour), Kind: "operation", Source: "Pax", Title: "old", Level: "info"})
	p.emit(config.HistoryEvent{ID: "recent", At: now.Add(-time.Minute), Kind: "operation", Source: "Pax", Title: "recent", Level: "info"})
	dir, err := os.MkdirTemp("", "pax-r-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(canonical, "p.sock")
	listener, err := ipc.ListenUser(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	server := grpc.NewServer()
	pb.RegisterPaxServiceServer(server, c)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	connection, err := grpc.NewClient("passthrough:///retention", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	client := pb.NewPaxServiceClient(connection)
	query := &pb.LogQuery{View: "activity", From: timestamppb.New(now.Add(-30 * time.Minute)), Until: timestamppb.New(now)}
	first, err := client.QueryLogs(ctx, query)
	if err != nil || first.Total != 1 {
		t.Fatal(first, err)
	}
	l := stopWriterGoroutine(t, c)
	cutoff := now.Add(-time.Hour)
	if _, err = l.logs.Prune(ctx, cutoff, historystore.Limits{Rows: 2000, Duration: time.Second}); err != nil {
		t.Fatal(err)
	}
	if err = l.bumpDeletion(ctx); err != nil {
		t.Fatal(err)
	}
	l.publishState(ctx)
	snapshot, err := client.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
	if err != nil || snapshot.GetHistory().GetLogRemovalBeforeMs() != cutoff.UnixMilli() {
		t.Fatal("removal scope missing on wire", snapshot.GetHistory(), err)
	}
	query.ReadToken = first.ReadToken
	kept, err := client.QueryLogs(ctx, query)
	if err != nil || kept.Total != 1 || kept.Records[0].RecordId != first.Records[0].RecordId {
		t.Fatal("unrelated removal interrupted real RPC", kept, err)
	}
	t.Logf("Unix RPC: removal bound=%d, recent total=%d, same token=%t", snapshot.History.LogRemovalBeforeMs, kept.Total, kept.ReadToken == first.ReadToken)
}
