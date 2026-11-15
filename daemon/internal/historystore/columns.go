package historystore

import (
	"context"
	"database/sql"
	"path/filepath"
)

// Columns owns the columnar log facts and their projections. Production Logs
// shares its writer; OpenColumns is also the isolated conversion/test entry.
type Columns struct {
	*store
	owner     *Logs
	snapshots *columnSnapshots
}

// ColumnsTx is valid only during Columns.Update; facts and projections share it.
type ColumnsTx struct {
	tx         *sql.Tx
	prepared   map[string]*sql.Stmt
	identities map[[32]byte]int64
	objects    map[columnObjectKey]int64
	events     map[string]bool
	optionIDs  map[string]int64
}

// Ordinary owner batches contain128 observations. Gap repair can touch more
// summaries in one transaction, so dictionary reuse also has a fixed cap.
const columnBatchDictionaryLimit = 1024

// OpenColumns opens column facts without running the production upgrade. This
// conversion entry never quarantines data or changes legacy metadata.
func OpenColumns(ctx context.Context, historyDir string, owner uint32) (*Columns, error) {
	if err := prepareDirectory(historyDir, owner); err != nil {
		return nil, err
	}
	path := filepath.Join(historyDir, logsFile)
	if err := guardFile(path, owner); err != nil {
		return nil, err
	}
	w, err := sql.Open("sqlite", dataSource(path, false))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	s := &store{path: path, w: w}
	ok := false
	defer func() {
		if !ok {
			_ = s.Close()
		}
	}()
	if err = s.quickCheck(ctx); err != nil {
		return nil, err
	}
	if _, err = w.ExecContext(ctx, "PRAGMA auto_vacuum=INCREMENTAL; PRAGMA journal_mode=WAL;"); err != nil {
		return nil, err
	}
	if err = initializeColumnFacts(ctx, s); err != nil {
		return nil, err
	}
	s.r, err = openReader(path, readerConns)
	if err != nil {
		return nil, err
	}
	ok = true
	return &Columns{store: s}, nil
}

// Update commits all facts, child rows and derived rows together, or none.
func (c *Columns) Update(ctx context.Context, fn func(*ColumnsTx) error) error {
	return c.snapshotUpdate(ctx, fn)
}

func (t *ColumnsTx) closeStatements() {
	for _, s := range t.prepared {
		_ = s.Close()
	}
}

func (t *ColumnsTx) statement(ctx context.Context, q string) (*sql.Stmt, error) {
	if s := t.prepared[q]; s != nil {
		return s, nil
	}
	s, e := t.tx.PrepareContext(ctx, q)
	if e == nil {
		t.prepared[q] = s
	}
	return s, e
}
func (t *ColumnsTx) exec(ctx context.Context, q string, args ...any) error {
	s, e := t.statement(ctx, q)
	if e != nil {
		return e
	}
	_, e = s.ExecContext(ctx, args...)
	return e
}
