package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// Invalid retired files must neither be opened nor stop the SQLite path.
// The backup is still retained; ticket89 never deletes user data.
func TestRetiredHistorySourcesStayUntouchedAcrossSQLiteRestart(t *testing.T) {
	ctx := context.Background()
	dir := newLogsTestDir(t)
	old := filepath.Join(dir, "observations")
	if err := os.Mkdir(old, 0700); err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{
		filepath.Join(old, "history.db"):                   "invalid retired bbolt; never import",
		filepath.Join(old, "history.db.migrated-20261027"): "retained backup",
		filepath.Join(dir, "route-usage.json"):             "invalid retired JSON; never import",
	}
	for path, body := range sources {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var versions [2]string
	for run := 0; run < 2; run++ {
		c := New("test", "test")
		c.ConfigureRouteData(dir, uint32(os.Geteuid()))
		if c.data.usageError != "" {
			t.Fatal("formal route-data assembly read retired usage JSON", c.data.usageError)
		}
		if err := ConfigureHistoryFixture(t, c, ctx, dir, uint32(os.Geteuid())); err != nil {
			t.Fatal(err)
		}
		if run == 0 {
			at := time.Now().Add(-time.Minute)
			f := config.FlowFact{Key: "sqlite/retired-source", StartedAt: at, ApplicationID: "fixture.app", Application: "Fixture", Domain: "fixture.example.test", RouteID: "r", RouteName: "Line", Direction: "proxy"}
			if err := FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "s", Collecting: true, Records: []config.Observation{{Session: "s", Sequence: 1, Kind: "started", At: at, Flow: &f, Upload: 10, Download: 90}}}); err != nil {
				t.Fatal(err)
			}
			if err := c.RecordEvent(ctx, config.HistoryEvent{ID: "retired-source-operation", At: at, Kind: "operation", Title: "saved operation", Level: "info"}); err != nil {
				t.Fatal(err)
			}
			logs, _ := c.logJob()
			if err := logs.Flush(ctx); err != nil {
				t.Fatal(err)
			}
			if err := c.trafficJob().store.Update(ctx, func(tx *historystore.TrafficTx) error {
				if err := tx.SetMeta(ctx, "migration_complete", "1"); err != nil {
					return err
				}
				if err := tx.SetMeta(ctx, "legacy_route_imported", "1"); err != nil {
					return err
				}
				return tx.SetMeta(ctx, trafficMetaCoverageFrom, fmt.Sprint(at.Add(-time.Hour).UnixMilli()))
			}); err != nil {
				t.Fatal(err)
			}
		}
		page, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: "today"})
		if err != nil || page.Upload != 10 || page.Download != 90 || page.Connections != 1 {
			t.Fatalf("run%d SQLite traffic changed: %v %v", run, page, err)
		}
		usage, err := c.QueryRouteUsage(ctx, &pb.RouteHistoryRequest{RouteId: "r", Window: "today"})
		if err != nil || usage.Upload != 10 || usage.Download != 90 {
			t.Fatalf("run%d route changed: %v %v", run, usage, err)
		}
		logs, err := c.QueryLogs(ctx, &pb.LogQuery{})
		if err != nil || len(logs.Items) == 0 {
			t.Fatalf("run%d logs missing: %v %v", run, logs, err)
		}
		tr := c.trafficJob()
		raw, _, err := tr.store.Meta(ctx, "traffic_revision")
		if err != nil {
			t.Fatal(err)
		}
		if run == 0 {
			versions[0] = raw
		} else {
			versions[1] = raw
			if versions[0] != versions[1] {
				t.Fatalf("restart changed revision %s/%s raw=%s", versions[0], versions[1], raw)
			}
		}
		if run == 1 {
			mark, _, err := tr.store.Meta(ctx, "migration_complete")
			if err != nil || mark != "1" {
				t.Fatal("existing metadata removed", mark, err)
			}
		}
		if err := CloseHistoryFixture(c, ctx); err != nil {
			t.Fatal(err)
		}
		for path, body := range sources {
			after, err := os.ReadFile(path)
			if err != nil || string(after) != body {
				t.Fatal("retired source or backup mutated", path, err)
			}
		}
	}
}

func TestFreshSQLiteStoresDoNotCreateImportState(t *testing.T) {
	c, _ := newTrafficRPCFixture(t)
	ctx := context.Background()
	tr := c.trafficJob()
	logs, _ := c.logJob()
	if err := tr.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := logs.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"migration_complete", "migration_pending", "migration_attempts", "migration_failed", "migration_source"} {
		if _, ok, err := tr.store.Meta(ctx, key); err != nil || ok {
			t.Fatal("fresh traffic wrote retired import state", key, ok, err)
		}
		if _, ok, err := logs.logs.Meta(ctx, key); err != nil || ok {
			t.Fatal("fresh logs wrote retired import state", key, ok, err)
		}
	}
}

func TestRetiredIncompleteImportMetadataRemainsPartial(t *testing.T) {
	ctx := context.Background()
	dir := newLogsTestDir(t)
	c := New("test", "test")
	if err := ConfigureHistoryFixture(t, c, ctx, dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	logs, _ := c.logJob()
	tr := c.trafficJob()
	if err := logs.logs.Update(ctx, func(tx *historystore.LogsTx) error { return tx.SetMeta(ctx, "migration_pending", "1") }); err != nil {
		t.Fatal(err)
	}
	if err := tr.store.Update(ctx, func(tx *historystore.TrafficTx) error { return tx.SetMeta(ctx, "migration_pending", "1") }); err != nil {
		t.Fatal(err)
	}
	if err := CloseHistoryFixture(c, ctx); err != nil {
		t.Fatal(err)
	}
	c = New("test", "test")
	if err := ConfigureHistoryFixture(t, c, ctx, dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = CloseHistoryFixture(c, ctx) }()
	logs, _ = c.logJob()
	meta, err := logs.logs.SnapshotMeta(ctx)
	if err != nil || !meta.Partial {
		t.Fatalf("retired log incompleteness hidden: %+v %v", meta, err)
	}
	snap, err := c.trafficSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snap.read.Close() }()
	if !snap.meta.Partial {
		t.Fatal("retired traffic incompleteness hidden")
	}
}
