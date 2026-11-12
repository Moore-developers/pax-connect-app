package historystore

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestSearchEvidenceSharesTransactionAndExactCutoff(t *testing.T) {
	ctx := context.Background()
	s := mustTraffic(t, historyDir(t))
	at := baseHour
	key := TrafficKey{BucketMs: at.UnixMilli(), Dimension: "domain", Object: "example.test", Route: "r", Direction: "proxy"}
	if err := s.Update(ctx, func(tx *TrafficTx) error {
		if err := tx.PutSearchEvidence(ctx, 0, key, "Earlier ÄPP"); err != nil {
			return err
		}
		cut := key
		cut.BucketMs += 1200
		if err := tx.PutSearchEvidence(ctx, 1, cut, "later ÄPP"); err != nil {
			return err
		}
		return tx.PutSearchEvidence(ctx, 0, key, "Earlier ÄPP")
	}); err != nil {
		t.Fatal(err)
	}
	fail := fmt.Errorf("rollback")
	if err := s.Update(ctx, func(tx *TrafficTx) error {
		if err := tx.PutSearchEvidence(ctx, 0, key, "rolled-back"); err != nil {
			return err
		}
		return fail
	}); err != fail {
		t.Fatal(err)
	}
	r, err := s.BeginRead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	count := func(res int, until time.Time, term string) int {
		t.Helper()
		n := 0
		if err := r.MatchingSearch(ctx, res, at, until, []string{"domain"}, term, func(TrafficKey) error { n++; return nil }); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count(0, at.Add(time.Minute), "äPP") != 1 || count(0, at.Add(time.Minute), "rolled-back") != 0 || count(1, at.Add(time.Second), "äPP") != 0 || count(1, at.Add(2*time.Second), "äPP") != 1 {
		t.Fatal("evidence dedupe/rollback/cutoff mismatch")
	}
}

func TestSearchEvidenceRetentionAndCapacityUseBucketFrontier(t *testing.T) {
	ctx := context.Background()
	s := mustTraffic(t, historyDir(t))
	at := baseHour
	if err := s.Update(ctx, func(tx *TrafficTx) error {
		for i := 0; i < 4; i++ {
			key := TrafficKey{BucketMs: at.Add(time.Duration(i) * time.Hour).UnixMilli(), Dimension: "route", Object: "r"}
			if err := tx.PutTraffic5m(ctx, TrafficRow{TrafficKey: key, UpBytes: 1}); err != nil {
				return err
			}
			for _, resolution := range []int{0, 1} {
				if err := tx.PutSearchEvidence(ctx, resolution, key, "kept"); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	limits := Limits{Rows: 2, Duration: time.Second}
	for {
		p, err := s.Prune(ctx, TrafficCutoffs{Aggregates: at.Add(time.Hour), Recent: at.Add(2 * time.Hour)}, limits)
		if err != nil {
			t.Fatal(err)
		}
		if !p.More {
			break
		}
	}
	var n int
	if err := s.Reader().QueryRow("SELECT count(*) FROM search_evidence").Scan(&n); err != nil || n != 5 {
		t.Fatal(n, err)
	}
	if _, err := s.EvictOldest(ctx, 0, Limits{Rows: 2, Duration: time.Second}); err != nil {
		t.Fatal(err)
	}
	if err := s.Reader().QueryRow("SELECT count(*) FROM search_evidence").Scan(&n); err != nil || n != 0 {
		t.Fatal("capacity left orphaned evidence", n, err)
	}
}
