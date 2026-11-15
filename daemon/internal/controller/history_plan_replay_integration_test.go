//go:build integration

package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// TestPlanJournalReplay verifies a real journal segment through both actual
// production owners. One second is preparation/diagnosis, not the M acceptance.
func TestPlanJournalReplay(t *testing.T) {
	source, dst := os.Getenv("PAX_PLAN_JOURNAL"), os.Getenv("PAX_PLAN_REPLAY_DEST")
	if source == "" || dst == "" {
		t.Skip("explicit private journal and new destination required")
	}
	if filepath.Dir(dst) != "/private/tmp" || !strings.HasPrefix(filepath.Base(dst), "pax-209-plan-replay-") {
		t.Fatal("replay destination must be a new /private/tmp/pax-209-plan-replay-* directory")
	}
	seconds, err := strconv.Atoi(os.Getenv("PAX_PLAN_REPLAY_SECONDS"))
	if err != nil || seconds < 1 || seconds > 300 {
		t.Fatal("PAX_PLAN_REPLAY_SECONDS must be between 1 and 300")
	}
	db, err := sql.Open("sqlite", "file:"+source+"?mode=ro&immutable=1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var earliest int64
	if err = db.QueryRow("SELECT min(at_ms) FROM observations").Scan(&earliest); err != nil {
		t.Fatal(err)
	}
	start := earliest
	if value := os.Getenv("PAX_PLAN_REPLAY_FROM_MS"); value != "" {
		start, err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.Query("SELECT session,seq,at_ms,kind,flow_key,body FROM observations WHERE at_ms>=? AND at_ms<? ORDER BY rowid", start, start+int64(seconds)*1000)
	if err != nil {
		t.Fatal(err)
	}
	var input []historystore.Observation
	kinds := map[string]int{}
	var expectedUp, expectedDown uint64
	for rows.Next() {
		var v historystore.Observation
		if err = rows.Scan(&v.Session, &v.Seq, &v.AtMs, &v.Kind, &v.FlowKey, &v.Body); err != nil {
			t.Fatal(err)
		}
		if v.Kind != journalPollKind && v.Kind != journalGapKind {
			var observation config.Observation
			if err = json.Unmarshal(v.Body, &observation); err != nil {
				t.Fatal(err)
			}
			if observation.Flow != nil && !config.IsDNSControl(*observation.Flow) {
				expectedUp += observation.Upload
				expectedDown += observation.Download
			}
		}
		input = append(input, v)
		kinds[v.Kind]++
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	if len(input) == 0 {
		t.Fatal("recorded segment is empty")
	}
	if err = os.Mkdir(dst, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := New("plan-real-replay", "plan-real-replay")
	if err = ConfigureHistoryFixture(t, c, ctx, dst, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = CloseHistoryFixture(c, context.Background()) }()
	logs, err := c.logJob()
	if err != nil {
		t.Fatal(err)
	}
	traffic := c.trafficJob()
	journal := traffic.journal.(*historystore.Journal)
	first := time.UnixMilli(start)
	wall := time.Now()
	var appended int
	for i := 0; i < len(input); {
		end := i + 1
		for end < len(input) && input[end].AtMs == input[i].AtMs {
			end++
		}
		due := wall.Add(time.Duration(input[i].AtMs-start) * time.Millisecond)
		if wait := time.Until(due); wait > 0 {
			time.Sleep(wait)
		}
		last := input[end-1]
		cursor := historystore.Cursor{Session: last.Session, Seq: last.Seq}
		if err = journal.Append(ctx, first.Add(time.Duration(last.AtMs-start)*time.Millisecond), input[i:end], cursor); err != nil {
			t.Fatal(err)
		}
		logs.poke()
		appended += end - i
		i = end
	}
	tail := historystore.Cursor{Session: input[len(input)-1].Session, Seq: input[len(input)-1].Seq}
	traffic.poke()
	deadline := time.Now().Add(120 * time.Second)
	for {
		logCursor, _, e := logs.logs.Cursor(ctx, logsCursorName)
		if e != nil {
			t.Fatal(e)
		}
		trafficCursor, _, e := traffic.store.Cursor(ctx, trafficCursorName)
		if e != nil {
			t.Fatal(e)
		}
		if logCursor == tail && trafficCursor == tail {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("log and traffic processors failed to reach the final input cursor")
		}
		time.Sleep(200 * time.Millisecond)
	}
	var gotUp, gotDown uint64
	if err = traffic.store.(*historystore.Traffic).Reader().QueryRow("SELECT coalesce(sum(up_bytes),0),coalesce(sum(down_bytes),0) FROM traffic_5m WHERE dimension='route'").Scan(&gotUp, &gotDown); err != nil {
		t.Fatal(err)
	}
	if gotUp != expectedUp || gotDown != expectedDown {
		t.Fatalf("real replay route bytes differ: got %d/%d want %d/%d", gotUp, gotDown, expectedUp, expectedDown)
	}
	t.Logf("PLAN_REPLAY seconds=%d records=%d types=%v byte_check=%d/%d cursor_matched=true elapsed=%s", seconds, appended, kinds, gotUp, gotDown, time.Since(wall).Round(time.Millisecond))
	fmt.Fprintln(os.Stderr, "PLAN_REPLAY: private diagnostic only; no 30-minute concurrency or UI acceptance claimed")
}
