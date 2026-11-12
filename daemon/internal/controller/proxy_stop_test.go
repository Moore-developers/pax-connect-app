package controller

import (
	"context"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"testing"
	"time"
)

func waitComplete(t *testing.T, c *Controller) *paxv1.Snapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for {
		snapshot, _ := c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
		if snapshot.PendingOperationId == "" {
			return snapshot
		}
		select {
		case <-ctx.Done():
			t.Fatal("operation timed out")
			return nil
		case <-time.After(time.Millisecond):
		}
	}
}
