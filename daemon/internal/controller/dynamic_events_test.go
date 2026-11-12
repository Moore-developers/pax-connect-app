package controller

import (
	"context"
	"encoding/json"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"strings"
	"testing"
	"time"
)

func TestUnregisteredEventCategoryAndDetailsSurviveReopen(t *testing.T) {
	ctx := context.Background()
	dir := newLogsTestDir(t)
	create := func() *Controller {
		c, _ := newLogsTestController(t, dir)
		return c
	}
	c := create()
	var e error
	event := config.HistoryEvent{ID: "new-category", At: time.Now(), Kind: "operation", Source: "fixture", Level: "info", Title: "策略更新", System: &config.SystemEvent{Type: "trust.policy_changed", Name: "更新信任策略", Category: config.EventCategory{Key: "trust", Name: "证书与信任", Symbol: "checkmark.shield"}, Result: "completed", Sections: []config.EventSection{{Kind: "changes", Title: "策略变化", Fields: []config.EventField{{Label: "策略", Before: "严格", After: "系统信任"}}}}}}
	if e = c.RecordEvent(ctx, event); e != nil {
		t.Fatal(e)
	}
	check := func(c *Controller) {
		q := &pb.LogQuery{View: "activity", Dimension: "system", Scope: "system"}
		d, e := c.QueryLogObjects(ctx, q)
		if e != nil || len(d.Items) != 1 || d.Items[0].Name != "证书与信任" || d.Items[0].Id != "fixture/trust" {
			t.Fatal("new category was discarded by fixed list", d, e)
		}
		q.ReadToken = d.ReadToken
		q.ObjectId = d.Items[0].Id
		p, e := c.QueryLogs(ctx, q)
		if e != nil || len(p.Records) != 1 || p.Records[0].GetSystemEvent().GetName() != "更新信任策略" {
			t.Fatal("new detail not projected", p, e)
		}
		v, e := c.GetLogDetail(ctx, &pb.LogDetailRequest{RecordId: p.Records[0].RecordId, Query: q})
		if e != nil || v.Record.GetSystemEvent().GetSections()[0].Fields[0].Before != "严格" {
			t.Fatal("event snapshot lost", v, e)
		}
	}
	check(c)
	stopLogsTestController(t, c)
	c = create()
	check(c)
}

func TestDynamicCategoriesAreNamespacedBoundedAndRevokedOnClear(t *testing.T) {
	ctx := context.Background()
	c, _ := newLogsTestController(t, newLogsTestDir(t))
	var e error
	for i, source := range []string{"a", "b"} {
		event := config.HistoryEvent{ID: source, At: time.Now(), Source: source, Kind: "operation", Level: "info", System: &config.SystemEvent{Type: "new.action", Name: "新动作", Category: config.EventCategory{Key: "new", Name: "相同名称"}, Result: "completed", Sections: []config.EventSection{{Kind: "facts", Title: "主要事实", Fields: []config.EventField{{Label: "访问令牌", Value: "must-not-survive"}, {Label: "来源", Value: "https://user:password@example.test/path?token=must-not-survive"}}}}}}
		if i == 0 {
			event.System.Name = "首次动作"
		}
		if e = c.RecordEvent(ctx, event); e != nil {
			t.Fatal(e)
		}
	}
	q := &pb.LogQuery{View: "activity", Dimension: "system", Scope: "system"}
	objects, e := c.QueryLogObjects(ctx, q)
	if e != nil || len(objects.Items) != 2 {
		t.Fatal("same-name sources merged", objects, e)
	}
	q.ReadToken = objects.ReadToken
	p, e := c.QueryLogs(ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range p.Records {
		raw, _ := json.Marshal(r.SystemEvent)
		if strings.Contains(string(raw), "must-not-survive") || strings.Contains(string(raw), "user:password") {
			t.Fatal("structured secret survived")
		}
	}
	invalid := config.HistoryEvent{ID: "oversize", At: time.Now(), Source: "a", System: &config.SystemEvent{Type: "new.action", Name: strings.Repeat("x", 257)}}
	if e = c.RecordEvent(ctx, invalid); e == nil {
		t.Fatal("unbounded event accepted")
	}
	if _, e = c.ClearLogs(ctx, &pb.ClearLogsRequest{OperationId: "clear-dynamic", RequestedAt: timestamppb.Now()}); e != nil {
		t.Fatal(e)
	}
	if _, e = c.GetLogDetail(ctx, &pb.LogDetailRequest{RecordId: p.Records[0].RecordId, Query: q}); e == nil {
		t.Fatal("stale detail survived clear")
	}
	q.ReadToken = ""
	objects, e = c.QueryLogObjects(ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	for _, o := range objects.Items {
		if o.Id == "a/new" || o.Id == "b/new" {
			t.Fatal("deleted dynamic category survived")
		}
	}
}

func TestUnknownStableKindsKeepDistinctReadableCategoryAndCleanedBounds(t *testing.T) {
	ctx := context.Background()
	c, _ := newLogsTestController(t, newLogsTestDir(t))
	var e error
	for _, kind := range []string{"trust_updated", "component_updated"} {
		if e = c.RecordEvent(ctx, config.HistoryEvent{ID: kind, Kind: kind, Source: "fixture", At: time.Now(), Level: "info", Title: "已接收"}); e != nil {
			t.Fatal(e)
		}
	}
	q := &pb.LogQuery{View: "activity", Scope: "system", Dimension: "system"}
	p, e := c.QueryLogObjects(ctx, q)
	if e != nil || len(p.Items) != 2 || p.Items[0].Name == p.Items[1].Name {
		t.Fatal("stable kinds lost readable names", p, e)
	}
	over := &config.SystemEvent{Type: "test.bounds", Name: "有界测试", Sections: []config.EventSection{{Kind: "facts", Title: "事实", Fields: []config.EventField{{Label: "文本", Value: strings.Repeat("token=x ", 250)}}}}}
	e = c.RecordEvent(ctx, config.HistoryEvent{ID: "expanded", Kind: "operation", Source: "fixture", At: time.Now(), System: over})
	if e == nil {
		t.Fatal("redaction expansion exceeded saved field budget")
	}
}
