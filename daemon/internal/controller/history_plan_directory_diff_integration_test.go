//go:build integration

package controller

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// The reference reads original catalog fields and applies the production
// domain/classification rules. It never consumes the optimized object rows.
func TestPlanRealDirectoryDifferential(t *testing.T) {
	dir, inventory := os.Getenv("PAX_PLAN_R_DIR"), os.Getenv("PAX_PLAN_INVENTORY")
	if dir == "" || inventory == "" {
		t.Skip("private real dataset and inventory required")
	}
	at, err := time.Parse(time.RFC3339, os.Getenv("PAX_PLAN_AT"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	logs, _, err := historystore.OpenLogs(ctx, dir, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logs.Close() }()
	logs.ConfigureColumnProjector(projectColumnBatch)
	c := New("plan-directory-reference", "plan-directory-reference")
	c.logs = &historyLogs{logs: logs}
	apps := planInventory(t, c, inventory)
	w := historystore.ColumnLogWindow{View: "activity", From: at.Add(-24 * time.Hour), Until: at, Cutoff: at.Add(-24 * time.Hour), Collection: "idle", Apps: apps}
	w.Classify = func(id, path string) (config.RoutingApplication, bool) { return userApplicationMatch(id, path, apps) }
	f := historystore.ColumnLogFilter{Dimension: "domain", Scope: "access"}
	snapshot, err := logs.Columns().Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	err = snapshot.WithRead(ctx, func(q historystore.ColumnQuery) error {
		project := func(ctx context.Context, s *historystore.ColumnLogSelection, key string) (historystore.ColumnGroup, error) {
			return projectColumnGroupStream(ctx, columnSelectionStream{s}, "activity", key, w.Until, w.Collection)
		}
		if _, e := historystore.PrepareColumnLogs(ctx, q, w, project); e != nil {
			return e
		}
		rows, e := q.QueryContext(ctx, `SELECT k,last_ns,coalesce(domain,''),user_app FROM log_catalog WHERE flow=1 AND NOT(flow=1 AND purpose='dns' AND level=1)`)
		if e != nil {
			return e
		}
		type group struct {
			value historystore.ColumnLogObject
			last  int64
			key   string
		}
		groups := map[string]group{}
		var total, unknown uint64
		for rows.Next() {
			var key, domain string
			var last, userApp int64
			if e = rows.Scan(&key, &last, &domain, &userApp); e != nil {
				_ = rows.Close()
				return e
			}
			total++
			id := config.MainDomain(domain)
			if id == "" {
				unknown++
				continue
			}
			v, exists := groups[id]
			v.value.Count++
			if !exists || last > v.last || last == v.last && key > v.key {
				v.value.ID, v.value.Name, v.value.Symbol = id, id, "gearshape"
				if userApp == 1 {
					v.value.Symbol = "app"
				}
				v.last, v.key = last, key
			}
			groups[id] = v
		}
		e = rows.Err()
		_ = rows.Close()
		if e != nil {
			return e
		}
		want := make([]historystore.ColumnLogObject, 0, len(groups))
		for _, value := range groups {
			want = append(want, value.value)
		}
		sort.Slice(want, func(i, j int) bool {
			if want[i].Count != want[j].Count {
				return want[i].Count > want[j].Count
			}
			a, b := strings.ToLower(want[i].Name), strings.ToLower(want[j].Name)
			if a != b {
				return a < b
			}
			return want[i].ID < want[j].ID
		})
		if len(want) == 0 {
			t.Fatal("real directory is empty")
		}
		w.DirectoryFilter = &f
		selection, e := historystore.PrepareColumnLogs(ctx, q, w, project)
		if e != nil {
			return e
		}
		pages := 0
		for offset := 0; offset < len(want); offset += 100 {
			items, gotTotal, gotUnknown, count, selected, er := selection.Objects(ctx, f, "", want[len(want)-1].ID, offset, 100)
			if er != nil {
				return er
			}
			if gotTotal != total || gotUnknown != unknown || int(count) != len(want) || selected == nil || *selected != want[len(want)-1] {
				t.Fatalf("directory metadata differs: total=%d/%d unknown=%d/%d objects=%d/%d", gotTotal, total, gotUnknown, unknown, count, len(want))
			}
			if len(items) != min(100, len(want)-offset) {
				t.Fatalf("directory page %d has %d items, expected %d", pages, len(items), min(100, len(want)-offset))
			}
			for i := range items {
				if items[i] != want[offset+i] {
					t.Fatalf("directory row differs at %d", offset+i)
				}
			}
			pages++
		}
		t.Logf("PLAN_REAL_DIRECTORY_DIFF rows=%d objects=%d pages=%d differences=0", total, len(want), pages)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
