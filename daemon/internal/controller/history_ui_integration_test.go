//go:build integration

package controller

import (
	"context"
	"fmt"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
	"os"
	"os/signal"
	"path/filepath"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/ipc"
	"syscall"
	"testing"
	"time"
)

// Explicit opt-in fixture drives the actual App socket contract without TUN or root.
// It is never assembled into either shipping executable.
func TestHistoryNativeUIFixture(t *testing.T) {
	dir := os.Getenv("PAX_HISTORY_UI_FIXTURE")
	if dir == "" {
		t.Skip("explicit local UI fixture only")
	}
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c := New("history-ui-fixture", "fixture")
	if err = ConfigureHistoryFixture(t, c, ctx, canonical, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = CloseHistoryFixture(c, context.Background()) }()
	snapshot0, err := c.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.UpdateSettings(ctx, &pb.UpdateSettingsRequest{
		Settings: &pb.Settings{LogRetentionHours: 24}, ExpectedRevision: snapshot0.Settings.Revision,
		FieldMask: []string{"log_retention_hours"}, OperationId: "fixture-retention", RequestedAt: timestamppb.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	// Freeze a synthetic installed-app catalogue; tests must not depend on this Mac's apps.
	var fixtureApps []config.RoutingApplication
	for i := 0; i < 13; i++ {
		fixtureApps = append(fixtureApps, config.RoutingApplication{ID: fmt.Sprintf("fixture.app.%02d", i), Name: fmt.Sprintf("受控测试应用 %02d", i), Path: fmt.Sprintf("/Applications/Fixture-%02d.app", i), Bundle: true})
	}
	c.history.applicationInventory = &historyApplicationInventory{At: time.Now(), Apps: fixtureApps}
	batch := config.ObservationBatch{Session: "ui-fixture", Collecting: true}
	flowIndex := 0
	appendFlows := func(count int, age time.Duration, newIdentity bool) {
		batch.Records = nil
		for i := 0; i < count; i++ {
			started := time.Now().UTC().Add(-age - time.Duration(i%50+2)*time.Second)
			closed := started.Add(time.Second)
			app := i % 12
			if newIdentity {
				app = 12
			}
			f := config.FlowFact{Key: fmt.Sprintf("ui-fixture/1/%d", flowIndex), ApplicationID: fmt.Sprintf("fixture.app.%02d", app), Application: fmt.Sprintf("受控测试应用 %02d", app), Process: fmt.Sprintf("/Applications/Fixture-%02d.app/Contents/MacOS/Fixture", app), ResponseKnown: true, ResponseMS: 12, Domain: fmt.Sprintf("target-%03d.example.test", flowIndex%110), IP: "192.0.2.10", Port: 443, Transport: "tcp", RouteID: "direct", RouteName: "直连", Direction: "direct", Rule: "ip_is_private", RuleSource: "内核规则", StartedAt: started, EstablishmentState: "ready", EstablishmentMS: 12, ClosedAt: &closed, Evidence: "controlled_ui_fixture"}
			flowIndex++
			for _, kind := range []string{"started", "sample", "closed"} {
				batch.HighWater++
				o := config.Observation{Session: batch.Session, Sequence: batch.HighWater, At: closed, Kind: kind, Flow: &f}
				if kind == "sample" {
					o.Upload = 1024
					o.Download = 8192
				}
				batch.Records = append(batch.Records, o)
			}
		}
		if err := FeedHistoryFixture(t, c, ctx, batch); err != nil {
			t.Fatal(err)
		}
	}
	appendFlows(140, 0, false)
	appendFlows(12, 30*time.Minute, false)
	appendFlows(12, 18*time.Hour, false)
	if err = c.RecordEvent(ctx, config.HistoryEvent{ID: "fixture-operation", At: time.Now(), Kind: "operation", Title: "受控联调记录", Level: "info", Source: "Pax", Raw: []string{"Authorization: Bearer must-not-appear"}}); err != nil {
		t.Fatal(err)
	}
	// The legacy global revision does not describe the split history stores.
	// Check the same read surfaces the App will use before publishing readiness.
	logQuery := &pb.LogQuery{View: "activity", Dimension: "application", Scope: "access", From: timestamppb.New(time.Now().Add(-24 * time.Hour)), Until: timestamppb.Now(), Limit: 100}
	logs, err := c.QueryLogs(ctx, logQuery)
	if err != nil || len(logs.GetRecords()) == 0 || logs.GetNextCursor() == "" || logs.GetReadToken() == "" {
		t.Fatalf("fixture logs not readable/paged: %v %v", logs, err)
	}
	objects, err := c.QueryLogObjects(ctx, logQuery)
	if err != nil || len(objects.GetItems()) < 12 {
		t.Fatalf("fixture log directory not readable: %v %v", objects, err)
	}
	traffic, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: "day"})
	if err != nil || len(traffic.GetItems()) < 12 || traffic.GetUpload() == 0 || traffic.GetDownload() == 0 || traffic.GetReadToken() == "" {
		t.Fatalf("fixture traffic not readable: %v %v", traffic, err)
	}
	listener, err := ipc.ListenUser(filepath.Join(canonical, "ui.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	server := grpc.NewServer()
	aiTick := configureAINativeUIFixture(t, c, ctx, canonical, server)
	pb.RegisterPaxServiceServer(server, c)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	if err = os.WriteFile(filepath.Join(canonical, "ready"), []byte(timestamppb.Now().AsTime().Format(time.RFC3339)), 0600); err != nil {
		t.Fatal(err)
	}
	// Control files live inside the private fixture root. No production socket or user data is used.
	// touch append-traffic => adds existing app 00..11 and new app 12; touch append-log => one operation.
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			aiTick()
			for _, action := range []string{"append-traffic", "append-log"} {
				path := filepath.Join(canonical, action)
				if _, err := os.Stat(path); os.IsNotExist(err) {
					continue
				} else if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if action == "append-traffic" {
					appendFlows(12, 0, false)
					appendFlows(1, 0, true)
				} else if err := c.RecordEvent(ctx, config.HistoryEvent{ID: fmt.Sprintf("fixture-operation-%d", time.Now().UnixNano()), At: time.Now(), Kind: "operation", Title: "受控新增操作", Level: "info", Source: "Pax"}); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path+".done", []byte(time.Now().Format(time.RFC3339Nano)), 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}
