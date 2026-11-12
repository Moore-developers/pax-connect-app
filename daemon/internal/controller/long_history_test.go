package controller_test

import (
	"context"
	"google.golang.org/protobuf/types/known/timestamppb"
	"os"
	"path/filepath"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/controller"
	"testing"
	"time"
)

func TestThirtyDayTrafficSurvivesDetailedFlowExpiryAndRestart(t *testing.T) {
	ctx := context.Background()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	c := controller.New("test", "test")
	if err = controller.ConfigureHistoryFixture(t, c, ctx, dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-10 * 24 * time.Hour)
	f := config.FlowFact{Key: "history/1/a", StartedAt: at, ApplicationID: "fixture.app", Application: "Fixture", Domain: "api.example.com", RouteID: "route-a", RouteName: "Route A", Direction: "proxy", EstablishmentState: "ready", ResponseKnown: true, ResponseMS: 120}
	closeAt := at.Add(time.Minute)
	closed := f
	closed.ClosedAt = &closeAt
	batch := config.ObservationBatch{Session: "history", Collecting: true, HighWater: 3, Records: []config.Observation{{Session: "history", Sequence: 1, At: at, Kind: "started", Flow: &f}, {Session: "history", Sequence: 3, At: at.Add(time.Second), Kind: "sample", Flow: &f, Upload: 100, Download: 900}, {Session: "history", Sequence: 3, At: closeAt, Kind: "closed", Flow: &closed}}}
	if err = controller.FeedHistoryFixture(t, c, ctx, batch); err != nil {
		t.Fatal(err)
	}
	if err = controller.FeedHistoryFixture(t, c, ctx, batch); err != nil {
		t.Fatal(err)
	}
	if err = controller.CloseHistoryFixture(c, ctx); err != nil {
		t.Fatal(err)
	}
	c = controller.New("test", "test")
	if err = controller.ConfigureHistoryFixture(t, c, ctx, dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if e := controller.CloseHistoryFixture(c, ctx); e != nil {
			t.Error(e)
		}
	}()
	page, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: "month"})
	if err != nil {
		t.Fatalf("30-day aggregates unavailable: %v", err)
	}
	if page.Upload != 100 || page.Download != 900 || len(page.Items) != 1 || page.Items[0].Connections != 1 {
		t.Fatalf("wrong aggregate bytes/count: up=%d down=%d rows=%d", page.Upload, page.Download, len(page.Items))
	}
	if !page.Items[0].LatencyKnown || page.Items[0].ValidLatencyCount != 1 {
		t.Fatal("response distribution lost")
	}
	week, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: "week"})
	if err != nil || week.Upload != 0 || len(week.Items) != 0 {
		t.Fatal("ten-day-old flow leaked into week", err)
	}
}

func TestAggregateCutoffAndLateIdentityDoNotLoseOrDuplicateTraffic(t *testing.T) {
	ctx := context.Background()
	dir, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	c := controller.New("test", "test")
	if e = controller.ConfigureHistoryFixture(t, c, ctx, dir, uint32(os.Geteuid())); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := controller.CloseHistoryFixture(c, ctx); e != nil {
			t.Error(e)
		}
	}()
	now := time.Now()
	at := now.Add(-10 * time.Second)
	f := config.FlowFact{Key: "late/1/a", StartedAt: at, Domain: "api.example.com", Direction: "direct", RouteID: "direct"}
	first := config.ObservationBatch{Session: "late", Collecting: true, Records: []config.Observation{{Session: "late", Sequence: 1, At: at, Kind: "started", Flow: &f}}}
	if e = controller.FeedHistoryFixture(t, c, ctx, first); e != nil {
		t.Fatal(e)
	}
	f.Rule, f.RuleSource = "final", "内核规则"
	if e = controller.FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "late", Collecting: true, Records: []config.Observation{{Session: "late", Sequence: 2, At: at.Add(500 * time.Millisecond), Kind: "phase", Flow: &f}}}); e != nil {
		t.Fatal(e)
	}
	f.ApplicationID = "fixture.app"
	f.Application = "Fixture"
	f.IP = "192.0.2.10"
	f.ResponseKnown = true
	f.ResponseMS = 80
	batch := config.ObservationBatch{Session: "late", Collecting: true, Records: []config.Observation{{Session: "late", Sequence: 3, At: at.Add(time.Second), Kind: "sample", Flow: &f, Upload: 10, Download: 90}, {Session: "late", Sequence: 4, At: now.Add(time.Second), Kind: "sample", Flow: &f, Upload: 100, Download: 900}}}
	if e = controller.FeedHistoryFixture(t, c, ctx, batch); e != nil {
		t.Fatal(e)
	}
	q := &pb.TrafficQuery{Dimension: "ip", Window: "today", Until: timestamppb.New(now)}
	page, e := c.QueryTraffic(ctx, q)
	if e != nil || page.Upload != 10 || page.Download != 90 || len(page.Items) != 1 {
		t.Fatalf("cutoff/IP incorrect up=%d down=%d rows=%d err=%v", page.GetUpload(), page.GetDownload(), len(page.GetItems()), e)
	}
	// Same cutoff after another write must reuse the first frozen source watermark.
	if e = controller.FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "late", Collecting: true, Records: []config.Observation{{Session: "late", Sequence: 5, At: now.Add(2 * time.Second), Kind: "sample", Flow: &f, Upload: 1000}}}); e != nil {
		t.Fatal(e)
	}
	q.Dimension = "application"
	app, e := c.QueryTraffic(ctx, q)
	if e != nil || len(app.Items) != 1 || app.UnknownSource == nil || app.UnknownSource.Connections != 1 || app.UnknownSource.Upload != 0 || app.UnknownSource.Download != 0 {
		t.Fatal("known source still duplicated as unknown", e)
	}
	if app.Items[0].Basis != "无" || len(app.Items[0].Bases) != 1 || app.Items[0].Bases[0].Connections != 0 || app.Items[0].Connections != 0 || app.Items[0].Upload != 10 || app.Items[0].Download != 90 {
		t.Fatalf("late attribution basis=%q entries=%v total=%d", app.Items[0].Basis, app.Items[0].Bases, app.Items[0].Connections)
	}
	q.Dimension = "network"
	network, e := c.QueryTraffic(ctx, q)
	if e != nil || network.Upload != 10 {
		t.Fatalf("dashboard watermarks differ: %d %v", network.GetUpload(), e)
	}
}
