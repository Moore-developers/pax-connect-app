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
	t.summaries = nil
	t.columns.identities = nil
	t.columns.objects = nil
	t.columns.events = nil
	t.columns.optionIDs = nil
	for _, table := range []string{"col_group_options", "col_option_values", "col_events", "col_connections", "col_event_index", "col_groups", "col_members", "col_objects", "col_answers", "col_raw", "col_texts", "col_sections", "col_fields", "col_identities", "col_traffic", "diagnostics"} {
		if _, err := t.tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("historystore: clear %s: %w", table, err)
		}
	}
	if _, err := t.tx.ExecContext(ctx, "UPDATE col_meta SET value='1' WHERE key='traffic_ready';UPDATE col_meta SET value='' WHERE key='traffic_progress'"); err != nil {
		return err
	}
	state, _, err := t.Meta(ctx, "column_upgrade_state")
	if err != nil {
		return err
	}
	if state == "building" || state == "failed" {
		// A clear applies to the old retained facts too. An in-flight import must
		// never resurrect them after the command's transaction commits.
		for _, table := range []string{"events", "connections"} {
			if _, err = t.tx.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
				return err
			}
		}
		if err = t.SetMeta(ctx, "column_upgrade_state", "ready"); err != nil {
			return err
		}
	}
	return nil
}
