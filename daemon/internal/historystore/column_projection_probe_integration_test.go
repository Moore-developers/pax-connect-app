//go:build integration

package historystore

import (
	"context"
	"os"
	"testing"
)

// Opt-in measurement uses the same Go SQLite writer defaults as the conversion.
func TestColumnProjectionStorageSettings(t *testing.T) {
	dir := os.Getenv("PAX_COLUMNS_PROJECTION_DEST")
	if dir == "" {
		t.Skip("PAX_COLUMNS_PROJECTION_DEST is not set")
	}
	ctx := context.Background()
	c, err := OpenColumns(ctx, dir, uid())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	for _, name := range []string{"secure_delete", "auto_vacuum", "wal_autocheckpoint", "page_size"} {
		var value int64
		if err = c.w.QueryRowContext(ctx, "PRAGMA "+name).Scan(&value); err != nil {
			t.Fatal(err)
		}
		t.Logf("COLUMN_WRITER_PRAGMA name=%s value=%d", name, value)
	}
	rows, err := c.w.QueryContext(ctx, "SELECT name,sum(pgsize) FROM dbstat WHERE name LIKE 'col_%' OR name LIKE 'sqlite_autoindex_col_%' GROUP BY name ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		var size int64
		if err = rows.Scan(&name, &size); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		t.Logf("COLUMN_TABLE_BYTES name=%s bytes=%d", name, size)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		t.Fatal(err)
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	var references, snapshots int64
	if err = c.w.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM col_events WHERE flow_present=1)+(SELECT count(*) FROM col_connections)").Scan(&references); err != nil {
		t.Fatal(err)
	}
	if err = c.w.QueryRowContext(ctx, "SELECT count(*) FROM (SELECT identity_id FROM col_events WHERE flow_present=1 UNION SELECT identity_id FROM col_connections)").Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	t.Logf("COLUMN_FACT_SNAPSHOTS references=%d unique=%d saved=%d", references, snapshots, references-snapshots)
}
