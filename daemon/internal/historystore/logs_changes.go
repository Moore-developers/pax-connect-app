package historystore

import (
	"context"
	"database/sql"
	"sync"
	"time"
)

// LogsChanges describes committed rows. Existing bodies are immutable to the receiver.
// Reset means retained identities were removed and a complete snapshot is required.
type LogsChanges struct {
	Connections map[string]LogConnection
	Events      []LogEvent
	Meta        LogsMeta
	Reset       bool
	// Positive only for a known time-prefix removal; zero means a full reset.
	RemovedBeforeMs int64
}

type logsReadObserver struct {
	sync.RWMutex
	publish func(context.Context, LogsChanges)
}

// ObserveReads installs the single owner's synchronous commit observer before readers start.
func (l *Logs) ObserveReads(fn func(context.Context, LogsChanges)) { l.observer.publish = fn }

// ReadBarrier orders acquisition of an in-memory version after a complete commit publication.
func (l *Logs) ReadBarrier(ctx context.Context, fn func()) error {
	for !l.observer.TryRLock() {
		timer := time.NewTimer(time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	defer l.observer.RUnlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	fn()
	return nil
}

// ReadSnapshot reads retained summaries and raw-free events in one short SQLite snapshot.
func (l *Logs) ReadSnapshot(ctx context.Context) (LogsChanges, error) {
	tx, err := l.r.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return LogsChanges{}, err
	}
	defer func() { _ = tx.Rollback() }()
	d := LogsChanges{Connections: map[string]LogConnection{}, Reset: true}
	d.Meta, err = readLogsMeta(ctx, tx)
	if err != nil {
		return d, err
	}
	rows, err := tx.QueryContext(ctx, connectionSelect)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		c, e := scanConnection(rows)
		if e != nil {
			_ = rows.Close()
			return d, e
		}
		d.Connections[c.FlowKey] = c
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return d, err
	}
	rows, err = tx.QueryContext(ctx, "SELECT seq,event_id,at_ms,body FROM events")
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var e LogEvent
		if err = rows.Scan(&e.Sequence, &e.EventID, &e.AtMs, &e.Body); err != nil {
			_ = rows.Close()
			return d, err
		}
		d.Events = append(d.Events, e)
	}
	err = rows.Err()
	_ = rows.Close()
	return d, err
}

func (l *Logs) publishRemoval(ctx context.Context, removed bool, beforeMs int64) {
	if removed && l.observer.publish != nil {
		l.observer.publish(ctx, LogsChanges{Reset: true, RemovedBeforeMs: beforeMs})
	}
}
