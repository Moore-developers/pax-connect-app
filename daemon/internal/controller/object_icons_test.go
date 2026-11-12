package controller

import (
	"bytes"
	"context"
	"google.golang.org/protobuf/types/known/timestamppb"
	"image"
	"image/png"
	"os"
	"path/filepath"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/siteicon"
	"sync/atomic"
	"testing"
	"time"
)

func TestObjectIconRPCExplicitFetchAndPrivacyClear(t *testing.T) {
	ctx := context.Background()
	dir := newLogsTestDir(t)
	c, _ := newLogsTestController(t, dir)
	var calls atomic.Int32
	var raw bytes.Buffer
	_ = png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 20, 20)))
	store, err := siteicon.New(filepath.Join(dir, "cache", "icons", "v1"), uint32(os.Geteuid()), func(context.Context, string) ([]byte, error) { calls.Add(1); return raw.Bytes(), nil })
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	c.ConfigureObjectIcons(store)
	hello, err := c.Hello(ctx, &pb.HelloRequest{ProtocolVersion: ProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, cap := range hello.Capabilities {
		found = found || cap == "objects.icons.v1"
	}
	if !found {
		t.Fatal("missing capability")
	}
	initial, _ := c.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
	q := &pb.ObjectIconRequest{Host: "example.com", LogDeletionGeneration: initial.History.LogDeletionGeneration}
	first, err := c.QueryObjectIcon(ctx, q)
	if err != nil || first.State != "missing" || calls.Load() != 0 {
		t.Fatal(first, err)
	}
	q.Fetch = true
	first, err = c.QueryObjectIcon(ctx, q)
	if err != nil || first.State != "ready" || len(first.Png) == 0 {
		t.Fatal(first, err)
	}
	if _, e := c.ClearLogs(ctx, &pb.ClearLogsRequest{OperationId: "invalid", RequestedAt: timestamppb.New(time.Now().Add(-48 * time.Hour))}); e == nil {
		t.Fatal("expired command accepted")
	}
	q.Fetch = false
	cached, e := c.QueryObjectIcon(ctx, q)
	if e != nil || cached.State != "ready" {
		t.Fatal("failed history transaction invalidated icon", cached, e)
	}
	if _, err = c.ClearLogs(ctx, &pb.ClearLogsRequest{OperationId: "clear-icon", RequestedAt: timestamppb.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.QueryObjectIcon(ctx, q); err == nil {
		t.Fatal("stale generation accepted")
	}
	snap, err := c.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	q.LogDeletionGeneration = snap.History.LogDeletionGeneration
	q.Fetch = false
	after, err := c.QueryObjectIcon(ctx, q)
	if err != nil || after.State != "missing" || calls.Load() != 1 {
		t.Fatal(after, err)
	}
}
