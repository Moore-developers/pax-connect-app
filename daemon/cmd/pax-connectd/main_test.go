package main

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/controller"
)

func TestShutdownWithActiveWatch(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	paxv1.RegisterPaxServiceServer(server, controller.New("test", ""))
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	watch, err := paxv1.NewPaxServiceClient(client).Watch(ctx, &paxv1.WatchRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := watch.Recv(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { stopServer(server, 50*time.Millisecond); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("shutdown hung with an active Watch")
	}
	if _, err := watch.Recv(); err == nil {
		t.Fatal("Watch remained open after shutdown")
	}
}
