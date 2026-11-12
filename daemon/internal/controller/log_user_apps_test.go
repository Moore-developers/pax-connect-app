package controller

import (
	"context"
	"fmt"
	"os"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"testing"
	"time"
)

func TestLogsApplicationsOnlyInstalledUserBundlesAndOthersRemainInSystem(t *testing.T) {
	ctx := context.Background()
	dir := newLogsTestDir(t)
	var e error
	c, _ := routingController(t)
	c.routing.applicationQuery = func(context.Context) []config.RoutingApplication {
		return []config.RoutingApplication{{ID: "installed.browser", Name: "Browser", Path: "/Applications/Browser.app", Bundle: true}}
	}
	if e := c.ConfigureHistory(ctx, dir, uint32(os.Geteuid())); e != nil {
		t.Fatal(e)
	}
	attachLogsPipeline(t, c, dir)
	at := time.Now().Add(-time.Minute)
	paths := []string{"/Applications/Browser.app/Contents/MacOS/Browser", "/Applications/Browser.app/Contents/Frameworks/Helper.app/Contents/MacOS/Helper", "/System/Applications/Safari.app/Contents/MacOS/Safari", "/usr/bin/curl", "/usr/libexec/trustd", ""}
	for i, path := range paths {
		f := config.FlowFact{Key: fmt.Sprint(i), Process: path, Application: "fixture", ApplicationID: "fixture", StartedAt: at, Domain: "example.test"}
		if e = c.RecordEvent(ctx, config.HistoryEvent{ID: fmt.Sprint(i), At: at, Kind: "started", Level: "info", Source: "sing-box", Flow: &f}); e != nil {
			t.Fatal(e)
		}
	}
	app, e := c.QueryLogs(ctx, &pb.LogQuery{View: "activity", Scope: "access", Dimension: "application"})
	if e != nil || app.Total != 2 {
		t.Fatalf("user application count=%d want2 err=%v", app.GetTotal(), e)
	}
	if app.Records[0].CategoryName != "Browser" || app.Records[0].Flow.Application != "fixture" {
		t.Fatal("main app projection must retain recorded identity evidence")
	}
	objectsApp, e := c.QueryLogObjects(ctx, &pb.LogQuery{View: "activity", Scope: "access", Dimension: "application", ReadToken: app.ReadToken})
	if e != nil || len(objectsApp.Items) != 1 || objectsApp.Items[0].Name != "Browser" || objectsApp.Items[0].Count != 2 {
		t.Fatal("helper not grouped under installed main app", e)
	}
	system, e := c.QueryLogs(ctx, &pb.LogQuery{View: "activity", Scope: "system", Dimension: "system"})
	if e != nil || system.Total != 4 {
		t.Fatalf("other sources count=%d want4 err=%v", system.GetTotal(), e)
	}
	objects, e := c.QueryLogObjects(ctx, &pb.LogQuery{View: "activity", Scope: "system", Dimension: "system"})
	if e != nil || objects.TotalEvents != 4 {
		t.Fatalf("system directory differs: %v", e)
	}
	domains, e := c.QueryLogs(ctx, &pb.LogQuery{View: "activity", Scope: "access", Dimension: "domain"})
	if e != nil || domains.Total != 6 {
		t.Fatal("domain history lost during recategorization", e)
	}
}

func TestCancelledApplicationScanDoesNotPoisonNextLogRead(t *testing.T) {
	c, _ := routingController(t)
	c.history = &historyOwner{}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	c.routing.applicationQuery = func(context.Context) []config.RoutingApplication {
		calls++
		if calls == 1 {
			cancel()
			return nil
		}
		return []config.RoutingApplication{{ID: "installed.app", Name: "Installed", Path: "/Applications/Installed.app", Bundle: true}}
	}
	if _, err := c.historyUserApplications(ctx); err == nil {
		t.Fatal("cancelled scan returned successful inventory")
	}
	if c.history.applicationInventory != nil {
		t.Fatal("cancelled scan cached partial inventory")
	}
	apps, err := c.historyUserApplications(context.Background())
	if err != nil || len(apps) != 1 || calls != 2 {
		t.Fatal("normal read reused partial inventory", err)
	}
}
