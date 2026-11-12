package controller

import (
	"context"
	"fmt"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"testing"
	"time"
)

func TestSystemDirectorySeparatesIndependentOperationsFromAccess(t *testing.T) {
	ctx := context.Background()
	c, _ := newLogsTestController(t, newLogsTestDir(t))
	var e error
	at := time.Now().Add(-time.Minute)
	events := []config.HistoryEvent{
		{ID: "switch", At: at, Kind: "operation", Source: "Pax", Title: "选择线路完成", Level: "info"},
		{ID: "switch-error", At: at, Kind: "operation", Source: "Pax", Title: "选择线路失败：invalid", Level: "error"},
		{ID: "power", At: at, Kind: "operation", Source: "Pax", Title: "关闭代理完成", Level: "info"},
		{ID: "diagnostic", At: at, Kind: "diagnostic", Source: "sing-box", Title: "内核错误", Level: "error"},
		{ID: "unknown-app", At: at, Kind: "failed", Source: "sing-box", Title: "连接失败", Level: "error", Flow: &config.FlowFact{Key: "access", Domain: "x.com", StartedAt: at, Failure: "connection_timeout"}},
	}
	for _, event := range events {
		if e = c.RecordEvent(ctx, event); e != nil {
			t.Fatal(e)
		}
	}
	// This is the existing public query seam: system is a directory dimension.
	q := &pb.LogQuery{View: "activity", Dimension: "system"}
	directory, e := c.QueryLogObjects(ctx, q)
	if e != nil || directory.TotalEvents != 4 || len(directory.Items) != 3 {
		t.Fatalf("system directory missing or polluted: %v %v", directory, e)
	}
}

func TestSystemDirectoryLevelCountsCategoryPagingAndScopeStayConsistent(t *testing.T) {
	ctx := context.Background()
	c, _ := newLogsTestController(t, newLogsTestDir(t))
	var e error
	at := time.Now().Add(-time.Hour)
	for i := 0; i < 105; i++ {
		level := "info"
		if i%2 == 0 {
			level = "error"
		}
		event := config.HistoryEvent{ID: fmt.Sprintf("route-%03d", i), At: at.Add(time.Duration(i) * time.Second), Kind: "operation", Source: "Pax", Title: "选择线路完成", Level: level}
		if e = c.RecordEvent(ctx, event); e != nil {
			t.Fatal(e)
		}
	}
	if e = c.RecordEvent(ctx, config.HistoryEvent{ID: "sing-box/kernel", At: at, Kind: "diagnostic", Source: "sing-box", Title: "内核错误", Level: "error"}); e != nil {
		t.Fatal(e)
	}
	q := &pb.LogQuery{View: "activity", Dimension: "system", Scope: "system", Level: "error", Limit: 100}
	objects, e := c.QueryLogObjects(ctx, q)
	if e != nil || objects.TotalEvents != 54 || len(objects.Items) != 2 || objects.Items[0].Id != "Pax/routes" || objects.Items[0].Count != 53 {
		t.Fatal("level directory counts wrong", objects, e)
	}
	q.ReadToken = objects.ReadToken
	q.ObjectId = "Pax/routes"
	page, e := c.QueryLogs(ctx, q)
	if e != nil || page.Total != 53 || len(page.Records) != 53 {
		t.Fatal("category row filter wrong", page, e)
	}
	q.Level = ""
	page, e = c.QueryLogs(ctx, q)
	if e != nil || page.Total != 105 || len(page.Records) != 100 {
		t.Fatal("category pagination wrong", page, e)
	}
	q.Cursor = page.NextCursor
	last, e := c.QueryLogs(ctx, q)
	if e != nil || len(last.Records) != 5 {
		t.Fatal(last, e)
	}
	q.Dimension = "application"
	q.Scope = "access"
	q.ObjectId = ""
	q.Cursor = ""
	access, e := c.QueryLogs(ctx, q)
	if e != nil || access.Total != 0 {
		t.Fatal("system records leaked into apps", access, e)
	}
	q.Dimension = "system"
	q.Scope = "system"
	q.Search = "内核"
	objects, e = c.QueryLogObjects(ctx, q)
	if e != nil || objects.TotalEvents != 106 || len(objects.Items) != 1 || objects.Items[0].Id != "sing-box/kernel" {
		t.Fatal("directory search changed all count", objects, e)
	}
}

func TestSelectedObjectProofComesFromWholeDirectoryNotFirstPageOrSearch(t *testing.T) {
	ctx := context.Background()
	c, _ := newLogsTestController(t, newLogsTestDir(t))
	var e error
	at := time.Now().Add(-time.Minute)
	for i := 0; i < 105; i++ {
		domain := fmt.Sprintf("host%03d.test", i)
		f := config.FlowFact{Key: domain, Domain: domain, StartedAt: at}
		if e = c.RecordEvent(ctx, config.HistoryEvent{ID: domain, At: at, Kind: "started", Level: "info", Source: "sing-box", Flow: &f}); e != nil {
			t.Fatal(e)
		}
	}
	q := &pb.LogQuery{View: "activity", Dimension: "domain", Scope: "access", ObjectId: "host104.test", Limit: 100}
	page, e := c.QueryLogObjects(ctx, q)
	if e != nil || len(page.Items) != 100 || page.SelectedObject.GetId() != "host104.test" {
		t.Fatal("valid second-page selection lost", page, e)
	}
	for _, o := range page.Items {
		if o.Id == "host104.test" {
			t.Fatal("fixture must put selection beyond first page")
		}
	}
	q.ReadToken = page.ReadToken
	q.Search = "no-match"
	hidden, e := c.QueryLogObjects(ctx, q)
	if e != nil || len(hidden.Items) != 0 || hidden.SelectedObject.GetId() != "host104.test" {
		t.Fatal("search erased selection evidence", hidden, e)
	}
	q.Level = "error"
	missing, e := c.QueryLogObjects(ctx, q)
	if e != nil || missing.SelectedObject != nil || missing.TotalEvents != 0 {
		t.Fatal("level-removal must invalidate selection despite search", missing, e)
	}
}
