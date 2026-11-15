//go:build integration

package historystore

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"
)

// Acceptance: the direct group-only COUNT/page plan equals the full catalog
// selection on real facts, including empty severity and first/middle/last pages.
// Full product field differences are covered independently by controller tests.
func TestColumnLogRealPagePlans(t *testing.T) {
	dir := os.Getenv("PAX_COLUMN_LOG_PLAN_DEST")
	if dir == "" {
		t.Skip("private converted target required")
	}
	ctx := context.Background()
	c, err := OpenColumns(ctx, dir, uid())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	snap, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snap.Close() }()
	var cases, keys int
	for _, view := range []string{"activity", "access", "problems", "operations"} {
		err = snap.WithRead(ctx, func(q ColumnQuery) error {
			w := ColumnLogWindow{View: view, Collection: "collecting", From: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), Cutoff: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), Until: time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC)}
			s, er := PrepareColumnLogs(ctx, q, w, func(context.Context, *ColumnLogSelection, string) (ColumnGroup, error) {
				return ColumnGroup{}, errors.New("unexpected boundary")
			})
			if er != nil {
				return er
			}
			for _, level := range []string{"", "info", "warning", "error"} {
				f := ColumnLogFilter{IncludeDNS: true, Level: level}
				count, er := s.Count(ctx, f)
				if er != nil {
					return er
				}
				where, args := s.where(f)
				var wantCount uint64
				if er = q.QueryRowContext(ctx, "SELECT count(*) FROM log_catalog l WHERE "+where, args...).Scan(&wantCount); er != nil {
					return er
				}
				if count != wantCount {
					t.Fatal(view, level, count, wantCount)
				}
				for _, offset := range []int{0, int(count) / 2, max(0, int(count)-20)} {
					got, er := s.KeysPage(ctx, f, offset, 20)
					if er != nil {
						return er
					}
					rows, er := q.QueryContext(ctx, "SELECT k FROM log_catalog l WHERE "+where+" ORDER BY last_ns DESC,k DESC LIMIT 20 OFFSET ?", append(append([]any{}, args...), offset)...)
					if er != nil {
						return er
					}
					var want []string
					for rows.Next() {
						var key string
						if er = rows.Scan(&key); er != nil {
							_ = rows.Close()
							return er
						}
						want = append(want, key)
					}
					er = rows.Err()
					_ = rows.Close()
					if er != nil {
						return er
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatal("page keys differ", view, level, offset)
					}
					cases++
					keys += len(got)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("COLUMN_LOG_PLAN_DIFF selected_pages=%d keys=%d differences=0", cases, keys)
}
