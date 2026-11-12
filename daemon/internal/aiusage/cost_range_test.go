package aiusage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOfficialCostRangeFiltersTotalsBeforePagination(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", "projects", "p", "cost.jsonl")
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	for i := range 14 {
		model := modelA
		if i < 2 {
			model = modelB
		}
		appendTo(t, path, lineAt(fmt.Sprintf("cost-%d", i), model, repriceNow.Add(time.Duration(i-20)*time.Minute)))
	}
	now := repriceNow
	store := openAt(t, home, &now, profileP(.25, nil))
	t.Cleanup(func() { _ = store.Close(context.Background()) })
	if err = store.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	bound := func(v int64) *int64 { return &v }
	query := Query{From: now.Add(-time.Hour).UnixMilli(), To: now.UnixMilli(), MinOfficialCost: bound(18_000_000), MaxOfficialCost: bound(18_000_000)}
	page, err := store.List(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if page.All != 12 || page.Total.Count != 12 || len(page.Requests) != 10 || page.Total.Cost != 54_000_000 {
		t.Fatalf("first page or channel totals wrong: %+v", page)
	}
	if page.Total.Costs[0]+page.Total.Costs[1]+page.Total.Costs[2]+page.Total.Costs[3] != 216_000_000 {
		t.Fatal("official total differs from filtered requests")
	}
	query.Page = 1
	next, err := store.List(context.Background(), query)
	if err != nil || next.All != 12 || next.Total != page.Total || len(next.Requests) != 2 {
		t.Fatalf("second page: %+v %v", next, err)
	}
	query.Page = 0
	query.MinOfficialCost = nil
	query.MaxOfficialCost = bound(6_000_000)
	small, err := store.List(context.Background(), query)
	if err != nil || small.All != 2 || small.Total.Count != 2 {
		t.Fatalf("upper-only range: %+v %v", small, err)
	}
	query.MaxOfficialCost = bound(0)
	zero, err := store.List(context.Background(), query)
	if err != nil || zero.All != 0 {
		t.Fatalf("explicit zero must not mean absent: %+v %v", zero, err)
	}
	for _, bounds := range [][2]*int64{{bound(-1), nil}, {nil, bound(-1)}, {bound(2), bound(1)}} {
		query.MinOfficialCost, query.MaxOfficialCost = bounds[0], bounds[1]
		if _, err = store.List(context.Background(), query); !errors.Is(err, ErrRange) {
			t.Fatalf("invalid bounds accepted: %v", err)
		}
	}
}
