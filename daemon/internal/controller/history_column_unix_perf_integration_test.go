//go:build integration

package controller

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/ipc"
)

// columnPerformanceClient sends the benchmark through a real private Unix
// socket and the production gRPC registration, with no live daemon or network.
func columnPerformanceClient(t *testing.T, c *Controller) pb.PaxServiceClient {
	t.Helper()
	dir, err := os.MkdirTemp("", "pax-209-perf-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "p.sock")
	listener, err := ipc.ListenUser(socket)
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterPaxServiceServer(server, c)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
		<-done
	})
	connection, err := grpc.NewClient("passthrough:///pax-209-perf", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return pb.NewPaxServiceClient(connection)
}
