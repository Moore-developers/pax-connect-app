package historystore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
)

// Cursor is a position in the core observation stream: the (session, seq) of the last consumed record.
// The zero value means "from the beginning".
type Cursor struct {
	Session string
	Seq     uint64
}

// IsZero reports whether the cursor has never advanced.
func (c Cursor) IsZero() bool { return c.Session == "" && c.Seq == 0 }

func (c Cursor) validate() error {
	if c.Seq > math.MaxInt64 {
		return ErrCursorRange
	}
	return nil
}

// queryer is the read surface shared by *sql.DB and *sql.Tx.
type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readMeta(ctx context.Context, q queryer, key string) (string, bool, error) {
	var value string
	err := q.QueryRowContext(ctx, "SELECT value FROM meta WHERE key=?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("historystore: meta %s: %w", key, err)
	}
	return value, true, nil
}

// Meta reads a meta value through the read pool.
func (s *store) Meta(ctx context.Context, key string) (string, bool, error) {
	return readMeta(ctx, s.r, key)
}

// Cursor reads the named job cursor through the read pool.
func (s *store) Cursor(ctx context.Context, name string) (Cursor, bool, error) {
	var c Cursor
	var seq int64
	err := s.r.QueryRowContext(ctx, "SELECT session, seq FROM job_cursor WHERE name=?", name).Scan(&c.Session, &seq)
	if errors.Is(err, sql.ErrNoRows) {
		return Cursor{}, false, nil
	}
	if err != nil {
		return Cursor{}, false, fmt.Errorf("historystore: cursor %s: %w", name, err)
	}
	c.Seq = uint64(seq)
	return c, true, nil
}

// baseTx is the part of a write transaction shared by logs.db and traffic.db.
type baseTx struct{ tx *sql.Tx }

// SetMeta upserts a meta value inside the transaction.
func (b baseTx) SetMeta(ctx context.Context, key, value string) error {
	return setMeta(ctx, b.tx, key, value)
}

// SetCursor upserts the named job cursor; commit it together with the rows it covers.
func (b baseTx) SetCursor(ctx context.Context, name string, c Cursor) error {
	if err := c.validate(); err != nil {
		return err
	}
	_, err := b.tx.ExecContext(ctx, "INSERT INTO job_cursor(name,session,seq) VALUES(?,?,?) ON CONFLICT(name) DO UPDATE SET session=excluded.session, seq=excluded.seq", name, c.Session, int64(c.Seq))
	if err != nil {
		return fmt.Errorf("historystore: set cursor %s: %w", name, err)
	}
	return nil
}

func setMeta(ctx context.Context, tx *sql.Tx, key, value string) error {
	if _, err := tx.ExecContext(ctx, "INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value); err != nil {
		return fmt.Errorf("historystore: set meta %s: %w", key, err)
	}
	return nil
}

// Meta reads metadata inside the current writer transaction.
func (b baseTx) Meta(ctx context.Context, key string) (string, bool, error) {
	return readMeta(ctx, b.tx, key)
}
