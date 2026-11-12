//go:build integration

package controller_test

import (
	"context"
	"errors"
	"net"
	"pax-connect/daemon/internal/config"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/controller"
)

func TestSubscriptionsDoNotChangeSnapshotVersion(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	state := controller.New("test", "")
	paxv1.RegisterPaxServiceServer(server, state)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	connection, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client := paxv1.NewPaxServiceClient(connection)
	initial, err := client.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	state.PublishSnapshot(&paxv1.Snapshot{Status: paxv1.ProtectionStatus_PROTECTION_STATUS_OFF})

	for range 2 {
		stream, err := client.Watch(ctx, &paxv1.WatchRequest{FromSeq: initial.SnapshotVersion})
		if err != nil {
			t.Fatal(err)
		}
		event, err := stream.Recv()
		if err != nil {
			t.Fatal(err)
		}
		if event.Seq != initial.SnapshotVersion+1 || event.SnapshotVersion != event.GetSnapshot().SnapshotVersion {
			t.Fatalf("subscription changed consistent version: initial=%d event=%v", initial.SnapshotVersion, event)
		}
	}
	after, err := client.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if after.SnapshotVersion != initial.SnapshotVersion+1 {
		t.Fatal("reading changed state version")
	}
}

// blockedStream models an HTTP/2 peer whose send window has stopped advancing.
type blockedStream struct {
	grpc.ServerStream
	ctx     context.Context
	entered chan struct{}
	release chan struct{}
	first   bool
}

func (s *blockedStream) Context() context.Context { return s.ctx }
func (s *blockedStream) Send(*paxv1.WatchEvent) error {
	if !s.first {
		s.first = true
		close(s.entered)
		select {
		case <-s.release:
		case <-s.ctx.Done():
			return s.ctx.Err()
		}
	}
	return nil
}

func TestSlowSubscriberIsDisconnectedAtBound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	state := controller.New("test", "")
	stream := &blockedStream{ctx: ctx, entered: make(chan struct{}), release: make(chan struct{})}
	result := make(chan error, 1)
	go func() { result <- state.Watch(&paxv1.WatchRequest{}, stream) }()
	<-stream.entered
	for range 65 {
		state.PublishSnapshot(&paxv1.Snapshot{Status: paxv1.ProtectionStatus_PROTECTION_STATUS_OFF})
	}
	defer close(stream.release)
	select {
	case err := <-result:
		if status.Code(err) != codes.ResourceExhausted {
			t.Fatalf("slow subscriber error = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("slow subscriber was not disconnected")
	}
	snapshot, err := state.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SnapshotVersion != 65 {
		t.Fatalf("state lost after overflow: %d", snapshot.SnapshotVersion)
	}
}

func TestGRPCSwitchPublishesAckSnapshotAndResult(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	state := controller.New("test", "")
	// A bad local profile is a safe end-to-end failure; the public RPC must still ACK and finish.
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	state.ConfigureOperations(lifetime, func() (config.DebugProfile, error) { return config.DebugProfile{}, errors.New("private error") })
	paxv1.RegisterPaxServiceServer(server, state)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	connection, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	ctx, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	client := paxv1.NewPaxServiceClient(connection)
	watch, err := client.Watch(ctx, &paxv1.WatchRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := watch.Recv(); err != nil {
		t.Fatal(err)
	}
	ack, err := client.SetProxyEnabled(ctx, &paxv1.SetProxyEnabledRequest{Enabled: true})
	if err != nil || ack.OperationId == "" {
		t.Fatalf("ack: %v", err)
	}
	for {
		event, err := watch.Recv()
		if err != nil {
			t.Fatal(err)
		}
		if result := event.GetOperation(); result != nil {
			if result.OperationId != ack.OperationId || result.Ok || result.Error.Code != "core_required" {
				t.Fatalf("result=%v", result)
			}
			break
		}
	}
	snapshot, err := client.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.PendingOperationId != "" || snapshot.Status != paxv1.ProtectionStatus_PROTECTION_STATUS_OFF {
		t.Fatal("failed command left pending/on")
	}
	shutdown, end := context.WithTimeout(context.Background(), time.Second)
	defer end()
	if err := state.CloseProxy(shutdown); err != nil {
		t.Fatal(err)
	}
}
