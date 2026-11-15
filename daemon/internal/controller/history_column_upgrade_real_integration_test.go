//go:build integration

package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// Acceptance: the production upgrade state machine, including its protected
// backup, actual import, all derived rows, cutover and old-table removal. Only
// an explicitly named pre-copied private database is opened for writing.
func TestColumnUpgradeReal(t *testing.T) {
	dir := os.Getenv("PAX_COLUMN_UPGRADE_DEST")
	if dir == "" {
		t.Skip("explicit private v3 copy required")
	}
	if filepath.Dir(dir) != "/private/tmp" {
		t.Fatal("upgrade evidence must use a private tmp copy")
	}
	ctx := context.Background()
	started := time.Now()
	logs, _, err := historystore.OpenLogs(ctx, dir, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logs.Close() }()
	logs.SetColumnProjector(projectColumnBatch)
	if err = logs.Ready(ctx); err == nil {
		t.Fatal("incomplete upgrade is queryable")
	}
	u := columnUpgradeJob{store: logs, owner: uint32(os.Geteuid())}
	maxStep := time.Duration(0)
	steps := 0
	var mainPeak, walPeak, totalPeak int64
	for !u.done {
		at := time.Now()
		if err = u.step(ctx); err != nil {
			t.Fatal(err)
		}
		maxStep = max(maxStep, time.Since(at))
		steps++
		main, wal := upgradeFileSize(filepath.Join(dir, "logs.db")), upgradeFileSize(filepath.Join(dir, "logs.db-wal"))
		mainPeak = max(mainPeak, main)
		walPeak = max(walPeak, wal)
		totalPeak = max(totalPeak, main+wal+upgradeFileSize(filepath.Join(dir, "logs.v3.backup.db")))
	}
	if err = logs.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("COLUMN_REAL_UPGRADE steps=%d elapsed_ms=%.3f max_step_ms=%.3f main_peak=%d wal_peak=%d with_backup_peak=%d", steps, float64(time.Since(started).Microseconds())/1000, float64(maxStep.Microseconds())/1000, mainPeak, walPeak, totalPeak)
	var legacyTables int
	if err = logs.Columns().Reader().QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('events','connections')").Scan(&legacyTables); err != nil || legacyTables != 0 {
		t.Fatalf("legacy tables survived %d %v", legacyTables, err)
	}
	source, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "logs.v3.backup.db")+"?mode=ro&immutable=1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Close() }()
	// Read original JSON, not the compatibility encoder under test, as the
	// independent whole-upgrade fact oracle.
	var events, connections int
	if err = logs.Columns().EachEvent(ctx, func(e config.HistoryEvent) error {
		var row struct {
			Sequence  int64
			Body, Raw []byte
		}
		er := source.QueryRowContext(ctx, "SELECT seq,body,raw FROM events WHERE event_id=?", e.ID).Scan(&row.Sequence, &row.Body, &row.Raw)
		if er != nil {
			return er
		}
		var v config.HistoryEvent
		if er = json.Unmarshal(row.Body, &v); er != nil {
			return er
		}
		v.Sequence = uint64(row.Sequence)
		if len(row.Raw) > 0 {
			if er = json.Unmarshal(row.Raw, &v.Raw); er != nil {
				return er
			}
		}
		if !reflect.DeepEqual(v, e) {
			t.Fatalf("event compatibility differs %s", e.ID)
		}
		events++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = logs.Columns().EachConnection(ctx, func(e config.LogConnectionSummary) error {
		var body []byte
		er := source.QueryRowContext(ctx, "SELECT body FROM connections WHERE flow_key=?", e.Fact.Key).Scan(&body)
		if er != nil {
			return er
		}
		var v config.LogConnectionSummary
		if er = json.Unmarshal(body, &v); er != nil {
			return er
		}
		if !reflect.DeepEqual(v, e) {
			t.Fatalf("summary compatibility differs %s", e.Fact.Key)
		}
		connections++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	checkColumnProjectionOracle(t, logs.Columns())
	t.Logf("COLUMN_REAL_UPGRADE_FACTS events=%d connections=%d differences=0", events, connections)
	var free int
	if err = logs.Columns().Reader().QueryRow("PRAGMA freelist_count").Scan(&free); err != nil || free != 0 {
		t.Fatalf("production upgrade did not reclaim pages: free=%d err=%v", free, err)
	}
	live, err := logs.LiveBytes(ctx)
	if err != nil || live > logsCapacityBytes {
		t.Fatalf("capacity bytes=%d err=%v", live, err)
	}
	t.Logf("COLUMN_REAL_UPGRADE_FINAL live_bytes=%d main_bytes=%d", live, upgradeFileSize(filepath.Join(dir, "logs.db")))
}
func upgradeFileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
