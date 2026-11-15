//go:build integration

package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// Export actual old/new first-20 candidates to the unchanged native Swift
// attention/changes functions. The Swift fixture is kept private, not in App.
func TestColumnDashboardCandidates(t *testing.T) {
	src, dst, out := os.Getenv("PAX_COLUMN_DASHBOARD_SOURCE"), os.Getenv("PAX_COLUMN_DASHBOARD_DEST"), os.Getenv("PAX_COLUMN_DASHBOARD_OUT")
	if src == "" || dst == "" || out == "" {
		t.Skip("explicit private source/destination/output required")
	}
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+src+"?mode=ro&immutable=1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query("SELECT seq,event_id,body FROM events")
	if err != nil {
		t.Fatal(err)
	}
	var events []config.HistoryEvent
	var until time.Time
	for rows.Next() {
		var row struct {
			EventID   string
			Sequence  int64
			Body, Raw []byte
		}
		if err = rows.Scan(&row.Sequence, &row.EventID, &row.Body); err != nil {
			t.Fatal(err)
		}
		e, er := decodeV3FixtureEvent(row.Sequence, row.Body, row.Raw)
		if er != nil {
			t.Fatal(er)
		}
		events = append(events, e)
		if e.At.After(until) {
			until = e.At
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	until = until.Add(time.Second)
	logs, _, err := historystore.OpenLogs(ctx, dst, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logs.Close() }()
	c := New("test", "test")
	c.logs = &historyLogs{logs: logs}
	c.history = &historyOwner{applicationInventory: &historyApplicationInventory{At: time.Now()}, clock: func() time.Time { return until }}
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	local := until.In(loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	if err = os.MkdirAll(out, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, items []*pb.ProductLog) {
		t.Helper()
		var values []json.RawMessage
		for _, p := range items {
			b, e := protojson.Marshal(p)
			if e != nil {
				t.Fatal(e)
			}
			values = append(values, b)
		}
		if values == nil {
			values = []json.RawMessage{}
		}
		b, e := json.Marshal(values)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(out, name+".json"), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	for _, w := range []struct {
		name string
		from time.Time
	}{{"", today}, {"-24h", until.Add(-24 * time.Hour)}} {
		var selected []config.HistoryEvent
		for _, e := range events {
			if e.At.After(w.from) && !e.At.After(until) {
				selected = append(selected, e)
			}
		}
		for _, view := range []string{"problems", "operations"} {
			q := &pb.LogQuery{View: view, From: timestamppb.New(w.from), Until: timestamppb.New(until), Limit: 20}
			old, err := projectLogView(ctx, selected, q)
			if err != nil {
				t.Fatal(err)
			}
			if len(old) > 20 {
				old = old[:20]
			}
			newer, err := c.QueryLogs(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			write("old-"+view+w.name, old)
			write("new-"+view+w.name, newer.Items)
			t.Logf("COLUMN_DASHBOARD_INPUT view=%s window=%s old=%d new=%d", view, w.name, len(old), len(newer.Items))
		}
	}
}

func decodeV3FixtureEvent(seq int64, body, raw []byte) (config.HistoryEvent, error) {
	var e config.HistoryEvent
	if err := json.Unmarshal(body, &e); err != nil {
		return e, err
	}
	e.Sequence = uint64(seq)
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &e.Raw); err != nil {
			return e, err
		}
	}
	return e, nil
}
