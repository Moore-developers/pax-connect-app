//go:build integration

package historystore

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"pax-connect/daemon/internal/config"
)

// TestColumnsRealRoundTrip is opt-in evidence against the read-only 1114 copy.
// No user data is consulted by an ordinary test run.
func TestColumnsRealRoundTrip(t *testing.T) {
	source := os.Getenv("PAX_COLUMNS_SOURCE")
	if source == "" {
		t.Skip("PAX_COLUMNS_SOURCE is not set")
	}
	ctx := context.Background()
	db, err := sql.Open("sqlite", dataSource(source, true)+"&immutable=1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	dir := os.Getenv("PAX_COLUMNS_DEST")
	if dir == "" {
		dir = historyDir(t)
	} else {
		dir, err = filepath.Abs(dir)
		if err != nil {
			t.Fatal(err)
		}
	}
	c, err := OpenColumns(ctx, dir, uid())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	var after int64
	events, connections, differences := 0, 0, 0
	decode := func(seq int64, body, raw []byte) config.HistoryEvent {
		var e config.HistoryEvent
		if er := json.Unmarshal(body, &e); er != nil {
			t.Fatal(er)
		}
		e.Sequence = uint64(seq)
		if len(raw) > 0 {
			if er := json.Unmarshal(raw, &e.Raw); er != nil {
				t.Fatal(er)
			}
		}
		return e
	}
	for {
		rows, er := db.QueryContext(ctx, "SELECT seq,body,raw FROM events WHERE seq>? ORDER BY seq LIMIT 128", after)
		if er != nil {
			t.Fatal(er)
		}
		var batch []config.HistoryEvent
		for rows.Next() {
			var seq int64
			var body, raw []byte
			if er = rows.Scan(&seq, &body, &raw); er != nil {
				t.Fatal(er)
			}
			batch = append(batch, decode(seq, body, raw))
			after = seq
		}
		er = rows.Err()
		_ = rows.Close()
		if er != nil {
			t.Fatal(er)
		}
		if len(batch) == 0 {
			break
		}
		if er = c.Update(ctx, func(tx *ColumnsTx) error {
			for _, e := range batch {
				if _, err2 := tx.PutEvent(ctx, e); err2 != nil {
					return err2
				}
			}
			return nil
		}); er != nil {
			t.Fatal(er)
		}
		for _, e := range batch {
			got, found, err2 := c.Event(ctx, e.ID)
			if err2 != nil || !found {
				t.Fatalf("event read: %v found %v", err2, found)
			}
			if !reflect.DeepEqual(got, e) {
				differences++
				t.Fatalf("event roundtrip differs id=%q", e.ID)
			}
			events++
		}
	}
	key := ""
	for {
		rows, er := db.QueryContext(ctx, "SELECT flow_key,body FROM connections WHERE flow_key>? ORDER BY flow_key LIMIT 128", key)
		if er != nil {
			t.Fatal(er)
		}
		var batch []config.LogConnectionSummary
		for rows.Next() {
			var body []byte
			if er = rows.Scan(&key, &body); er != nil {
				t.Fatal(er)
			}
			var s config.LogConnectionSummary
			if er = json.Unmarshal(body, &s); er != nil {
				t.Fatal(er)
			}
			batch = append(batch, s)
		}
		er = rows.Err()
		_ = rows.Close()
		if er != nil {
			t.Fatal(er)
		}
		if len(batch) == 0 {
			break
		}
		if er = c.Update(ctx, func(tx *ColumnsTx) error {
			for _, s := range batch {
				if err2 := tx.PutConnection(ctx, s); err2 != nil {
					return err2
				}
			}
			return nil
		}); er != nil {
			t.Fatal(er)
		}
		for _, s := range batch {
			got, found, err2 := c.Connection(ctx, s.Fact.Key)
			if err2 != nil || !found {
				t.Fatalf("connection read: %v found %v", err2, found)
			}
			if !reflect.DeepEqual(got, s) {
				differences++
				t.Fatalf("connection roundtrip differs key=%q", s.Fact.Key)
			}
			connections++
		}
	}
	t.Logf("COLUMN_ROUNDTRIP events=%d connections=%d differences=%d destination=%s", events, connections, differences, dir)
}
