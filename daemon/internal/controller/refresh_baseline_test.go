package controller_test

import (
	"context"
	"google.golang.org/grpc"
	"os"
	"path/filepath"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/controller"
	"sync/atomic"
	"testing"
	"time"
)

type refreshWatch struct {
	grpc.ServerStream
	ctx       context.Context
	snapshots atomic.Int64
	first     chan struct{}
}

func (s *refreshWatch) Context() context.Context { return s.ctx }
func (s *refreshWatch) Send(e *pb.WatchEvent) error {
	if e.GetSnapshot() != nil {
		if s.snapshots.Add(1) == 1 {
			close(s.first)
		}
	}
	return nil
}
func TestRefreshIdleBaseline(t *testing.T) {
	if os.Getenv("PAX_REFRESH_BASELINE") != "1" {
		t.Skip("explicit baseline observation")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(dir, 0700)
	c := controller.New("test", "test")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err = c.ConfigureHistory(ctx, dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseHistory(context.Background()) }()
	stream := &refreshWatch{ctx: ctx, first: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- c.Watch(&pb.WatchRequest{}, stream) }()
	<-stream.first
	time.Sleep(2200 * time.Millisecond)
	cancel()
	<-done
	t.Logf("idle2.2s full snapshots=%d (includes initial)", stream.snapshots.Load())
}

func TestIdleHistoryDoesNotRepublishFullSnapshots(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(dir, 0700)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := controller.New("test", "test")
	if err = c.ConfigureHistory(ctx, dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseHistory(context.Background()) }()
	stream := &refreshWatch{ctx: ctx, first: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- c.Watch(&pb.WatchRequest{}, stream) }()
	<-stream.first
	time.Sleep(1150 * time.Millisecond)
	cancel()
	<-done
	if stream.snapshots.Load() != 1 {
		t.Fatalf("idle history republished %d complete snapshots", stream.snapshots.Load())
	}
}
