package historystore

import (
	"context"
	"reflect"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
)

func TestColumnProductTextSurvivesReopenAndFrozenRead(t *testing.T) {
	ctx := context.Background()
	dir := historyDir(t)
	c, err := OpenColumns(ctx, dir, uid())
	if err != nil {
		t.Fatal(err)
	}
	original := config.HistoryEvent{ID: "meaning", Sequence: 1, At: time.Now().UTC(), Kind: "operation", Title: "旧文案", Source: "Pax",
		TitleText: &config.ProductText{Code: "event.saved", Params: map[string]string{"route": "用户线路"}},
		System: &config.SystemEvent{Type: "route.edit", Name: "编辑线路", NameText: &config.ProductText{Code: "event.route.edit"},
			Category: config.EventCategory{Key: "routes", Name: "线路", NameText: &config.ProductText{Code: "event.category.routes"}},
			Sections: []config.EventSection{{Kind: "changes", Title: "线路变化", TitleText: &config.ProductText{Code: "event.section.route_changes"},
				Fields: []config.EventField{{Label: "线路", Before: "旧用户名称", After: "新用户名称", LabelText: &config.ProductText{Code: "event.field.route"}}}}}}}
	legacy := config.HistoryEvent{ID: "legacy", Sequence: 2, At: original.At, Title: "原文", Source: "external"}
	if err = c.Update(ctx, func(tx *ColumnsTx) error {
		if _, err := tx.PutEvent(ctx, original); err != nil {
			return err
		}
		_, err := tx.PutEvent(ctx, legacy)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	c, err = OpenColumns(ctx, dir, uid())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	for _, want := range []config.HistoryEvent{original, legacy} {
		got, found, err := c.Event(ctx, want.ID)
		if err != nil || !found || !reflect.DeepEqual(got, want) {
			t.Fatalf("roundtrip mismatch: %v %#v %#v", err, got, want)
		}
	}
	snapshot, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := snapshot.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err = c.Update(ctx, func(tx *ColumnsTx) error {
		_, err := tx.tx.ExecContext(ctx, "DELETE FROM col_texts WHERE event_id=?", original.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	got, found, err := snapshot.Event(ctx, original.ID)
	if err != nil || !found || !reflect.DeepEqual(got, original) {
		t.Fatalf("frozen text lost: %v %#v", err, got)
	}
	if count(t, c.r, "SELECT count(*) FROM col_events") != 2 {
		t.Fatal("text change altered fact count")
	}
}
