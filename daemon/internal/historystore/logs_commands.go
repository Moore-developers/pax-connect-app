package historystore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// LogCommand is one idempotent log command receipt: its stable id, request signature and
// arrival time. The result column is reserved and not read by the current commands.
type LogCommand struct {
	ID        string
	Signature string
	AtMs      int64
}

// Command reads one receipt through the write transaction.
func (t *LogsTx) Command(ctx context.Context, id string) (LogCommand, bool, error) {
	var c LogCommand
	err := t.tx.QueryRowContext(ctx, "SELECT id,signature,at_ms FROM commands WHERE id=?", id).
		Scan(&c.ID, &c.Signature, &c.AtMs)
	if errors.Is(err, sql.ErrNoRows) {
		return LogCommand{}, false, nil
	}
	if err != nil {
		return LogCommand{}, false, fmt.Errorf("historystore: command %s: %w", id, err)
	}
	return c, true, nil
}

// PutCommand records a command receipt in the same transaction as its effect.
func (t *LogsTx) PutCommand(ctx context.Context, c LogCommand) error {
	_, err := t.tx.ExecContext(ctx, "INSERT INTO commands(id,signature,at_ms) VALUES(?,?,?)", c.ID, c.Signature, c.AtMs)
	if err != nil {
		return fmt.Errorf("historystore: put command: %w", err)
	}
	return nil
}

// PruneCommands deletes receipts older than beforeMs and returns how many remain.
func (t *LogsTx) PruneCommands(ctx context.Context, beforeMs int64) (int, error) {
	if _, err := t.tx.ExecContext(ctx, "DELETE FROM commands WHERE at_ms< ?", beforeMs); err != nil {
		return 0, fmt.Errorf("historystore: prune commands: %w", err)
	}
	var n int
	if err := t.tx.QueryRowContext(ctx, "SELECT count(*) FROM commands").Scan(&n); err != nil {
		return 0, fmt.Errorf("historystore: count commands: %w", err)
	}
	return n, nil
}

// ClearLogTables deletes every connection, event and diagnostic row. The event sequence in
// sqlite_sequence is deliberately kept: a cleared log keeps its head watermark.
func (t *LogsTx) ClearLogTables(ctx context.Context) error {
	if t.changes != nil {
		t.changes.Reset = true
	}
	for _, table := range []string{"events", "connections", "diagnostics"} {
		if _, err := t.tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("historystore: clear %s: %w", table, err)
		}
	}
	return nil
}

// ConnectionsPage returns up to limit summaries with flow_key greater than after, ordered
// by key. Startup normalization walks the table in bounded transactions with it.
func (t *LogsTx) ConnectionsPage(ctx context.Context, after string, limit int) ([]LogConnection, error) {
	if limit <= 0 {
		return nil, ErrInvalidLimits
	}
	rows, err := t.tx.QueryContext(ctx, connectionSelect+" WHERE flow_key>? ORDER BY flow_key LIMIT ?", after, limit)
	if err != nil {
		return nil, fmt.Errorf("historystore: connections page: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []LogConnection
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("historystore: connections page: %w", err)
	}
	return out, nil
}
