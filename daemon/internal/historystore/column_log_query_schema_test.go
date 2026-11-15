package historystore

import (
	"context"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
)

// Acceptance: development version 2 upgrades its query-only option columns
// from saved flow values, is repeatable, and preserves the returned group row.
func TestColumnGroupOptionSchemaUpgrade(t *testing.T) {
	ctx := context.Background()
	c := mustColumnsImportTarget(t, historyDir(t))
	if err := c.EnsureProjections(ctx); err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	g := ColumnGroup{View: "activity", Key: "g", EventID: "e", FirstAt: at, LastAt: at, Until: at, Count: 1, Flow: &ColumnProjectedFlow{Fact: config.FlowFact{Key: "flow", Application: "Browser", RouteName: "Direct"}}}
	if err := c.Update(ctx, func(tx *ColumnsTx) error {
		if err := tx.PutGroup(ctx, g); err != nil {
			return err
		}
		_, err := tx.tx.ExecContext(ctx, "UPDATE col_meta SET value='2' WHERE key='projection_version';DROP INDEX col_groups_window;")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := c.EnsureProjections(ctx); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err := c.Group(ctx, "activity", "g")
	if err != nil || !ok || got.Application != "Browser" || got.RouteName != "Direct" {
		t.Fatal(got, ok, err)
	}
	if got.Flow.Fact.Application != g.Flow.Fact.Application || got.Flow.Fact.RouteName != g.Flow.Fact.RouteName {
		t.Fatal("saved flow changed")
	}
	var version string
	if err = c.Reader().QueryRow("SELECT value FROM col_meta WHERE key='projection_version'").Scan(&version); err != nil || version != "5" {
		t.Fatal(version, err)
	}
}

func TestColumnOptionsDictionaryClearAndWriteSameTransaction(t *testing.T) {
	ctx := context.Background()
	l := mustLogs(t, historyDir(t))
	g := ColumnGroup{View: "activity", Key: "g", Application: "App", RouteName: "Route", RulePackage: "Rule", FirstAt: baseHour, LastAt: baseHour, Until: baseHour, Count: 1, Flow: &ColumnProjectedFlow{}}
	if err := l.Update(ctx, func(tx *LogsTx) error {
		if err := tx.columns.PutGroup(ctx, g); err != nil {
			return err
		}
		if err := tx.ClearLogTables(ctx); err != nil {
			return err
		}
		if err := tx.columns.PutGroup(ctx, g); err != nil {
			return err
		}
		var n int
		err := tx.tx.QueryRowContext(ctx, "SELECT count(*) FROM col_group_options g JOIN col_option_values a ON a.id=g.app_id JOIN col_option_values r ON r.id=g.route_id JOIN col_option_values p ON p.id=g.rule_id").Scan(&n)
		if err == nil && n != 1 {
			t.Fatal("clear reused removed options", n)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
