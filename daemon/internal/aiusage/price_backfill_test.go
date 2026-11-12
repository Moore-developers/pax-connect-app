package aiusage

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Real failure: the model was added to the tariff table after its first requests
// had already been persisted without prices. Reopening must finish those rows.
func TestOpenBackfillsMissingPricesAndRestoresPreviousDay(t *testing.T) {
	ctx := context.Background()
	now := local(2026, 11, 11, 12, 0)
	h := home(t)
	s := openAt(t, h, &now, profileP(.9, nil))
	prior := local(2026, 11, 9, 20, 0)
	short, long := int64(0), int64(42878)
	missing := Request{CLI: "claude", Session: "fable", Key: "before-tariff", At: prior.UnixMilli(), Model: "claude-fable-5-2", Input: 42880, Write: 42878, Output: 4852, ProfileID: "historical", ProfileName: "Historical", Multiplier: .3, Details: Details{WriteShort: &short, WriteLong: &long, PriceVersion: "before-fable"}}
	current := Request{CLI: "claude", Session: "current", Key: "priced", At: prior.AddDate(0, 0, 1).UnixMilli(), Model: "claude-fable-5-2", Input: 1000, Multiplier: .16}
	current.price()
	// A pre-existing priced row must keep its historical tariff, even if different.
	current.Details.Prices[0] = &current.Multiplier
	unknown := Request{CLI: "codex", Session: "unknown", Key: "unknown", At: prior.AddDate(0, 0, -3).UnixMilli(), Model: "no-price", Input: 500, Multiplier: 1}
	synthetic := Request{CLI: "claude", Session: "local", Key: "synthetic", At: unknown.At, Model: syntheticModel, Multiplier: 1}
	if err := s.commit(ctx, []Request{missing, current, unknown, synthetic}, cursor{Path: "historical"}); err != nil {
		t.Fatal(err)
	}
	before := s.State()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	s = openAt(t, h, &now, profileP(.9, nil))
	t.Cleanup(func() { _ = s.Close(ctx) })
	result, err := s.AnalyzeLimits(ctx, prior.AddDate(0, 0, 1).Add(-20*time.Hour).UnixMilli(), prior.AddDate(0, 0, 2).Add(-20*time.Hour).UnixMilli(), nil)
	if err != nil || result.Limits.Summary.Comparison == nil {
		t.Fatalf("previous day still unavailable after known model price backfill: %+v %v", result.Limits.Summary.Comparison, err)
	}
	closeLimit(t, result.Limits.Summary.Comparison.Input.Official, .686064)
	expected := missing
	expected.price()
	tx, err := s.r.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []Request{expected, current, unknown, synthetic} {
		got, exists, e := readRequest(ctx, tx, want.CLI, want.Key)
		if e != nil || !exists || !reflect.DeepEqual(got, want) {
			t.Fatalf("stored request changed incorrectly: key=%s got=%+v want=%+v err=%v", want.Key, got, want, e)
		}
	}
	_ = tx.Rollback()
	var requests, tokens, official, billed, keys int64
	if err := s.r.QueryRowContext(ctx, "SELECT sum(count),sum(input),sum(c0+c1+c2+c3),sum(cost) FROM usage_hourly").Scan(&requests, &tokens, &official, &billed); err != nil {
		t.Fatal(err)
	}
	if err := s.r.QueryRowContext(ctx, "SELECT count(*) FROM request_keys").Scan(&keys); err != nil {
		t.Fatal(err)
	}
	if requests != 4 || keys != 4 || tokens != 44380 || official != expected.Costs[0]+expected.Costs[2]+expected.Costs[3]+current.Costs[0] || billed != expected.Cost+current.Cost {
		t.Fatalf("bucket/key mismatch: %d %d %d %d %d", requests, keys, tokens, official, billed)
	}
	if s.State().Revision != before.Revision+1 || s.State().Generation != before.Generation {
		t.Fatalf("wrong invalidation: before=%+v after=%+v", before, s.State())
	}
	after := s.State()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	s = openAt(t, h, &now, nil)
	if s.State() != after {
		t.Fatal("reopening rewrote prices again")
	}
}

func TestOpenMissingPriceBackfillRollsBackOnBucketFailure(t *testing.T) {
	ctx := context.Background()
	now := local(2026, 11, 11, 12, 0)
	h := home(t)
	s := openAt(t, h, &now, nil)
	r := Request{CLI: "claude", Session: "fable", Key: "missing", At: now.AddDate(0, 0, -1).UnixMilli(), Model: "claude-fable-5-2", Input: 1000, Multiplier: .3}
	if err := s.commit(ctx, []Request{r}, cursor{Path: "before-backfill"}); err != nil {
		t.Fatal(err)
	}
	before := s.State().Revision
	if _, err := s.w.ExecContext(ctx, "CREATE TRIGGER reject_price_delta BEFORE INSERT ON usage_hourly BEGIN SELECT RAISE(ABORT, 'test bucket failure'); END"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(ctx, Options{Directory: filepath.Join(h, "state"), Home: h, Owner: uint32(os.Geteuid()), Now: func() time.Time { return now }})
	if opened != nil || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("failed backfill did not stop opening: %v %v", opened, err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(h, "state", "ai_usage.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var missing, cost, revision int64
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM requests WHERE input_price IS NULL AND cost=0 AND c0=0").Scan(&missing); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT sum(cost) FROM usage_hourly").Scan(&cost); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key='revision'").Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if missing != 1 || cost != 0 || uint64(revision) != before {
		t.Fatalf("partial price repair survived rollback: %d %d %d", missing, cost, revision)
	}
}
