//go:build integration

package controller

import (
	"context"
	"os"
	"testing"
	"time"

	"pax-connect/daemon/internal/historystore"
)

func TestColumnLogActivityProfile(t *testing.T) {
	dir := os.Getenv("PAX_COLUMN_LOG_PROFILE_DEST")
	if dir == "" {
		t.Skip("private upgraded target required")
	}
	ctx := context.Background()
	c, err := historystore.OpenColumns(ctx, dir, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if err = c.EnsureProjections(ctx); err != nil {
		t.Fatal(err)
	}
	var ns int64
	if err = c.Reader().QueryRow("SELECT max(at_ns) FROM col_events").Scan(&ns); err != nil {
		t.Fatal(err)
	}
	until := time.Unix(0, ns).Add(time.Second)
	for _, span := range []time.Duration{24 * time.Hour, time.Hour} {
		w := historystore.ColumnLogWindow{View: "activity", From: until.Add(-span), Until: until, Cutoff: until.Add(-24 * time.Hour), Collection: "collecting"}
		snap, err := c.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		err = snap.WithRead(ctx, func(q historystore.ColumnQuery) error {
			start := time.Now()
			s, er := historystore.PrepareColumnLogs(ctx, q, w, func(ctx context.Context, s *historystore.ColumnLogSelection, key string) (historystore.ColumnGroup, error) {
				return projectColumnGroupStream(ctx, columnSelectionStream{s}, "activity", key, w.Until, w.Collection)
			})
			if er != nil {
				return er
			}
			t.Log("prepare", span, time.Since(start))
			f := historystore.ColumnLogFilter{IncludeDNS: true}
			if os.Getenv("PAX_COLUMN_LOG_PROFILE_UI") == "1" {
				f = historystore.ColumnLogFilter{Scope: "access", Dimension: "domain"}
			}
			start = time.Now()
			n, er := s.Count(ctx, f)
			if er != nil {
				return er
			}
			t.Log("count", span, n, time.Since(start))
			start = time.Now()
			keys, er := s.KeysPage(ctx, f, 0, 20)
			if er != nil {
				return er
			}
			t.Log("keys", span, time.Since(start))
			start = time.Now()
			for _, key := range keys {
				g, _, e := s.Group(ctx, key)
				if e != nil {
					return e
				}
				if _, _, e = s.Event(ctx, g.EventID, false); e != nil {
					return e
				}
			}
			t.Log("page", span, time.Since(start))
			start = time.Now()
			_, _, _, er = s.Options(ctx)
			t.Log("options", span, time.Since(start))
			return er
		})
		_ = snap.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}
