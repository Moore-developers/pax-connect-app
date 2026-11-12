package controller

import (
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"testing"
	"time"
)

func TestHistoryFixSnapshotDeadlineWhileWriterLocked(t *testing.T) {
	h := newTrafficHarness(t)
	c := New("test", "test")
	c.traffic = h.tr
	h.tr.snapshotMu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		s, e := c.trafficSnapshot(ctx)
		if s != nil {
			_ = s.read.Close()
		}
		done <- e
	}()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("deadline did not fire")
	}
	select {
	case err := <-done:
		h.tr.snapshotMu.Unlock()
		if err != context.DeadlineExceeded {
			t.Fatalf("expected cancelled snapshot, got %v", err)
		}
		return
	case <-time.After(80 * time.Millisecond):
		h.tr.snapshotMu.Unlock()
		<-done
		t.Fatal("snapshot ignored its deadline while writer held commit lock")
	}
}
func TestHistoryFixQueryTrafficDeadlineWhileWriterLocked(t *testing.T) {
	h := newTrafficHarness(t)
	c := New("test", "test")
	c.traffic = h.tr
	h.tr.snapshotMu.Lock()
	done := make(chan error, 1)
	go func() {
		_, e := c.QueryTraffic(context.Background(), &pb.TrafficQuery{Dimension: "application", Window: "today"})
		done <- e
	}()
	select {
	case e := <-done:
		h.tr.snapshotMu.Unlock()
		if status.Code(e) != codes.DeadlineExceeded {
			t.Fatalf("expected deadline error: %v", e)
		}
		return
	case <-time.After(2200 * time.Millisecond):
	}
	select {
	case e := <-done:
		h.tr.snapshotMu.Unlock()
		t.Logf("returned by 2.2s: %v", e)
	default:
		h.tr.snapshotMu.Unlock()
		<-done
		t.Fatal("QueryTraffic still blocked 2.2s after its internal 2s deadline")
	}
}
