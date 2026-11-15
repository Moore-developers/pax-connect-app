//go:build integration

package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// TestColumnLogRPCPerformance includes the Controller timeout, snapshot,
// options/problem groups and token checks. PAX_COLUMN_PERF_UNIX=1 adds real
// Unix gRPC transport; neither mode claims App UI timing.
func TestColumnLogRPCPerformance(t *testing.T) {
	dir := os.Getenv("PAX_COLUMN_LOG_PERF_DEST")
	if dir == "" {
		t.Skip("private completed upgrade directory required")
	}
	ctx := context.Background()
	store, _, err := historystore.OpenLogs(ctx, dir, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	store.ConfigureColumnProjector(projectColumnBatch)
	liveBytes, err := store.LiveBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("COLUMN_LOG_RPC_SOURCE live_bytes=%d", liveBytes)
	c := New("test", "test")
	c.logs = &historyLogs{logs: store}
	c.history = &historyOwner{applicationInventory: &historyApplicationInventory{At: time.Now()}}
	query := c.QueryLogs
	transport := "controller"
	if os.Getenv("PAX_COLUMN_PERF_UNIX") == "1" {
		client := columnPerformanceClient(t, c)
		query = func(ctx context.Context, q *pb.LogQuery) (*pb.LogPage, error) { return client.QueryLogs(ctx, q) }
		transport = "unix-grpc"
	}
	inputs, err := store.Reader().Query("SELECT DISTINCT application_id,process FROM col_identities WHERE application_id<>'' AND process LIKE '/Applications/%' ORDER BY application_id,process LIMIT 128")
	if err != nil {
		t.Fatal(err)
	}
	for inputs.Next() {
		var id, path string
		if err = inputs.Scan(&id, &path); err != nil {
			t.Fatal(err)
		}
		c.history.applicationInventory.Apps = append(c.history.applicationInventory.Apps, config.RoutingApplication{ID: id, Name: filepath.Base(path), Path: path, Bundle: true})
	}
	if err = inputs.Err(); err != nil {
		t.Fatal(err)
	}
	_ = inputs.Close()
	t.Logf("COLUMN_LOG_RPC_INVENTORY recorded_matching_inputs=%d total=256", len(c.history.applicationInventory.Apps))
	for i := len(c.history.applicationInventory.Apps); i < 256; i++ {
		c.history.applicationInventory.Apps = append(c.history.applicationInventory.Apps, config.RoutingApplication{ID: fmt.Sprintf("test.inventory.%03d", i), Name: fmt.Sprintf("Installed %03d", i), Path: fmt.Sprintf("/Applications/Installed %03d.app", i), Bundle: true})
	}
	var ns int64
	if err = store.Reader().QueryRow("SELECT max(at_ns) FROM col_events").Scan(&ns); err != nil {
		t.Fatal(err)
	}
	until := time.Unix(0, ns).Add(time.Second)
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	local := until.In(loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	// Keep the observed retained facts admissible at the frozen test clock. The
	// production method uses the real retention wall clock, which would discard
	// this prior-night frozen fixture as development proceeds.
	c.history.clock = func() time.Time { return until }
	for _, view := range []string{"", "activity", "access", "problems", "operations"} {
		if chosen := os.Getenv("PAX_COLUMN_LOG_PERF_VIEWS"); chosen != "" && !strings.Contains(","+chosen+",", ","+view+",") {
			continue
		}
		for _, window := range []struct {
			name string
			from time.Time
		}{{"today", today}, {"24h", until.Add(-24 * time.Hour)}} {
			variants := []*pb.LogQuery{{IncludeDns: true}}
			if view == "activity" && os.Getenv("PAX_COLUMN_LOG_PERF_UI") == "1" {
				variants = append(variants, &pb.LogQuery{Dimension: "domain", Scope: "access"}, &pb.LogQuery{Dimension: "application", Scope: "access"}, &pb.LogQuery{Dimension: "system", Scope: "system"})
			}
			for _, q := range variants {
				q.View, q.From, q.Until, q.Limit = view, timestamppb.New(window.from), timestamppb.New(until), 20
				var elapsed []float64
				for i := 0; i < 20; i++ {
					at := time.Now()
					page, e := query(ctx, q)
					if e != nil {
						t.Fatalf("view=%s window=%s %v", view, window.name, e)
					}
					elapsed = append(elapsed, float64(time.Since(at).Microseconds())/1000)
					if page.Total == 0 && view == "" {
						t.Fatal("fixture fell outside retention; use frozen clock hook")
					}
				}
				sort.Float64s(elapsed)
				t.Logf("COLUMN_LOG_RPC_PERF transport=%s view=%s window=%s dimension=%s scope=%s include_dns=%t inventory=256 samples=20 p50_ms=%.3f p95_ms=%.3f max_ms=%.3f", transport, view, window.name, q.Dimension, q.Scope, q.IncludeDns, elapsed[9], elapsed[18], elapsed[19])
				if elapsed[18] > 200 {
					t.Errorf("full RPC P95 exceeds 200ms: view=%s window=%s dim=%s scope=%s p95=%.3f", view, window.name, q.Dimension, q.Scope, elapsed[18])
				}
			}
		}
	}
}
