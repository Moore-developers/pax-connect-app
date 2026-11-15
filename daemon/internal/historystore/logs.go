package historystore

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
)

const (
	logsFile    = "logs.db"
	logsVersion = 4
)

// Meta keys written by the log job. Values are integers except error_code and the
// generation tokens; log_cleared_at is Unix milliseconds.
const (
	MetaRetentionHours        = "retention_hours"
	MetaSettingsRevision      = "settings_revision"
	MetaLogsRevision          = "logs_revision"
	MetaLogDeletionGeneration = "log_deletion_generation"
	MetaLogRemovalBefore      = "log_removal_before_ms"
	MetaSummaryGeneration     = "summary_generation"
	MetaClearGeneration       = "clear_generation"
	MetaLogClearedAt          = "log_cleared_at"
	MetaJournalGeneration     = "journal_generation"
	MetaPartial               = "partial"
	MetaErrorCode             = "error_code"
	MetaCapacityEvicted       = "capacity_evicted"
)

// LogDiagnostic is one scrubbed diagnostic text of a connection.
type LogDiagnostic struct {
	FlowKey string
	Seq     int64
	AtMs    int64
	Text    string
}

// Logs is the 24-hour log database (logs.db).
type Logs struct {
	*store
	columns   *Columns
	importer  *ColumnImport
	projector func(context.Context, *ColumnsTx, ColumnImportBatch) error
}

// LogsTx is the write transaction handed to Logs.Update; it is valid only inside the callback.
type LogsTx struct {
	baseTx
	columns   *ColumnsTx
	batch     *ColumnImportBatch
	summaries map[string]columnSummaryEntry
}

// OpenLogs opens or creates logs.db under historyDir. A corrupt file is renamed aside and
// replaced (see Recovered); an unsupported schema version returns ErrSchemaVersion.
func OpenLogs(ctx context.Context, historyDir string, owner uint32) (*Logs, Recovered, error) {
	if err := prepareDirectory(historyDir, owner); err != nil {
		return nil, Recovered{}, err
	}
	s, recovered, err := openColumnLogs(ctx, filepath.Join(historyDir, logsFile), owner)
	if err != nil {
		return nil, recovered, err
	}
	l := &Logs{store: s}
	l.columns = &Columns{store: s, owner: l}
	pruneQuarantine(historyDir, regexp.MustCompile(`^logs\.db$`))
	return l, recovered, nil
}

// Columns returns the same file and writer used by the production log owner.
func (l *Logs) Columns() *Columns { return l.columns }

// ConfigureColumnProjector installs the controller's production rules before the
// owner starts. Its callback shares the fact transaction and may not reenter Logs.
func (l *Logs) ConfigureColumnProjector(fn func(context.Context, *ColumnsTx, ColumnImportBatch) error) {
	l.projector = fn
}

// SetColumnProjector binds the production projection before owner startup.
func (l *Logs) SetColumnProjector(fn func(context.Context, *ColumnsTx, ColumnImportBatch) error) {
	l.projector = fn
}

// ErrColumnProjection distinguishes a derived-row failure from a fact writer
// failure. The failed transaction has rolled back before the owner sees it.
var ErrColumnProjection = errors.New("historystore: column projection failed")

// DisableColumnProjection records an explicit query-side failure. The owner
// replays its uncommitted journal batch afterward, keeping facts and collection
// moving without rebuilding or repeatedly invoking the failing projector.
func (l *Logs) DisableColumnProjection(ctx context.Context, reason string) error {
	return l.Update(ctx, func(tx *LogsTx) error {
		if err := tx.SetMeta(ctx, "column_projection_state", "failed"); err != nil {
			return err
		}
		return tx.SetMeta(ctx, "column_projection_error", reason)
	})
}

// Update runs fn in one write transaction and commits it, or rolls back when fn fails.
func (l *Logs) Update(ctx context.Context, fn func(*LogsTx) error) error {
	return l.columns.snapshotUpdate(ctx, func(ct *ColumnsTx) error {
		batch := ColumnImportBatch{}
		t := &LogsTx{baseTx: baseTx{ct.tx}, columns: ct, batch: &batch}
		if err := fn(t); err != nil {
			return err
		}
		if l.projector != nil && (len(batch.Events) > 0 || len(batch.Connections) > 0 || len(batch.RemovedEvents) > 0 || len(batch.RemovedConnections) > 0) {
			state, _, err := t.Meta(ctx, "column_projection_state")
			if err != nil {
				return err
			}
			if state != "failed" {
				if err := l.projector(ctx, ct, batch); err != nil {
					return fmt.Errorf("%w: %w", ErrColumnProjection, err)
				}
			}
		}
		if len(batch.RemovedEvents) > 0 || len(batch.RemovedConnections) > 0 {
			return ct.pruneColumnOrphans(ctx, 128)
		}
		return nil
	})
}

func (t *LogsTx) advanceHead(ctx context.Context, seq int64) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES('column_head_sequence',?) ON CONFLICT(key) DO UPDATE SET value=CAST(max(CAST(value AS INTEGER),CAST(excluded.value AS INTEGER)) AS TEXT)`, strconv.FormatInt(seq, 10))
	return err
}

// NextEventSequence preserves the durable high water even after clear/deletion.
func (t *LogsTx) NextEventSequence(ctx context.Context) (int64, error) {
	var next int64
	err := t.tx.QueryRowContext(ctx, "SELECT coalesce((SELECT CAST(value AS INTEGER) FROM meta WHERE key='column_head_sequence'),0)+1").Scan(&next)
	return next, err
}

// EventExists checks immutable new-table identity without decoding an event.
func (t *LogsTx) EventExists(ctx context.Context, id string) (bool, error) {
	exists, err := t.columns.columnEventExists(ctx, id)
	if err != nil || exists {
		return exists, err
	}
	state, _, err := t.Meta(ctx, "column_upgrade_state")
	if err != nil || state != "building" && state != "failed" {
		return false, err
	}
	err = t.tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM events WHERE event_id=?)", id).Scan(&exists)
	if err == nil && exists {
		t.columns.rememberEvent(id, true)
	}
	return exists, err
}

// PutDiagnostic inserts or replaces the diagnostic at (FlowKey, Seq).
func (t *LogsTx) PutDiagnostic(ctx context.Context, d LogDiagnostic) error {
	_, err := t.tx.ExecContext(ctx, "INSERT OR REPLACE INTO diagnostics(flow_key,seq,at_ms,text) VALUES(?,?,?,?)",
		d.FlowKey, d.Seq, d.AtMs, d.Text)
	return wrapPut("diagnostic", err)
}

// TrimDiagnostics keeps only the newest keep diagnostics of one connection.
func (t *LogsTx) TrimDiagnostics(ctx context.Context, flowKey string, keep int) error {
	_, err := t.tx.ExecContext(ctx, `DELETE FROM diagnostics WHERE flow_key=? AND seq NOT IN
 (SELECT seq FROM diagnostics WHERE flow_key=? ORDER BY seq DESC LIMIT ?)`, flowKey, flowKey, keep)
	if err != nil {
		return fmt.Errorf("historystore: trim diagnostics: %w", err)
	}
	return nil
}

func wrapPut(what string, err error) error {
	if err != nil {
		return fmt.Errorf("historystore: put %s: %w", what, err)
	}
	return nil
}
