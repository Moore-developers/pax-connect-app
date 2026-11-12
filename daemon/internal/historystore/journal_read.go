package historystore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

// openForRead opens a one-connection read-only pool on an hour file. It returns
// nil without error for a missing or zero-byte uninitialised file. Corrupt
// and unsupported files return errors for capture-owner recovery.
func (j *Journal) openForRead(ctx context.Context, hour time.Time) (*sql.DB, error) {
	path := j.path(hour)
	exists, err := guardExisting(path, j.owner)
	if err != nil || !exists {
		return nil, err
	}
	if info, e := os.Stat(path); e != nil {
		return nil, e
	} else if info.Size() == 0 {
		return nil, nil
	}
	db, err := openReader(path, 1)
	if err != nil {
		return nil, err
	}
	if err = checkVersion(ctx, db, journalVersion); err != nil {
		_ = db.Close()
		if errors.Is(err, ErrSchemaVersion) {
			return nil, ErrSchemaVersion
		}
		return nil, err
	}
	return db, nil
}

// drop renames a corrupt hour file aside (closing the writer if it holds it) and records the gap.
func (j *Journal) drop(ctx context.Context, hour time.Time, reason string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.writer != nil && j.writerHour.Equal(hour) {
		if err := j.closeWriter(); err != nil {
			return err
		}
	}
	dropped, err := j.loseLocked(ctx, hour, reason)
	if err != nil {
		return err
	}
	if dropped.ID != "" {
		j.dropped = append(j.dropped, dropped)
	}
	return nil
}

// Cursor returns the ingest cursor stored with the newest valid hour file. A corrupt
// newest file is dropped and the previous one is used; files without a cursor
// (empty) are skipped. The second result is false when no file holds a cursor.
func (j *Journal) Cursor(ctx context.Context) (Cursor, bool, error) {
	files, err := j.Files()
	if err != nil {
		return Cursor{}, false, err
	}
	for i := len(files) - 1; i >= 0; i-- {
		c, ok, err := j.fileCursor(ctx, files[i].Hour)
		if isCorrupt(err) || errors.Is(err, ErrSchemaVersion) {
			reason := "corrupt"
			if errors.Is(err, ErrSchemaVersion) {
				reason = "schema"
			}
			if err = j.drop(ctx, files[i].Hour, reason); err != nil {
				return Cursor{}, false, err
			}
			continue
		}
		if err != nil || ok {
			return c, ok, err
		}
	}
	return Cursor{}, false, nil
}

func (j *Journal) fileCursor(ctx context.Context, hour time.Time) (Cursor, bool, error) {
	db, err := j.openForRead(ctx, hour)
	if err != nil || db == nil {
		return Cursor{}, false, err
	}
	defer func() { _ = db.Close() }()
	if err = quickCheck(ctx, db); err != nil {
		return Cursor{}, false, err
	}
	session, okSession, err := readMeta(ctx, db, metaCursorSess)
	if err != nil {
		return Cursor{}, false, err
	}
	raw, okSeq, err := readMeta(ctx, db, metaCursorSeq)
	if err != nil || !okSession || !okSeq {
		return Cursor{}, false, err
	}
	seq, err := strconv.ParseUint(raw, 10, 63)
	if err != nil {
		return Cursor{}, false, fmt.Errorf("historystore: journal cursor: %w", err)
	}
	return Cursor{Session: session, Seq: seq}, true, nil
}

// ReadAfter returns up to limit records after cursor, oldest first, across hour files in
// order (within a file by append order). The zero cursor, or a cursor whose record is no
// longer in any file, starts at the oldest file. Damaged files return JournalDamage without moving the caller cursor.
func (j *Journal) ReadAfter(ctx context.Context, after Cursor, limit int) ([]Observation, error) {
	state, err := j.losses()
	if err != nil {
		return nil, err
	}
	if len(state.Pending) > 0 {
		return nil, ErrJournalRecoveryPending
	}
	if limit <= 0 {
		return nil, ErrInvalidLimits
	}
	if err := after.validate(); err != nil {
		return nil, err
	}
	files, err := j.Files()
	if err != nil {
		return nil, err
	}
	first, rowid := 0, int64(0)
	if !after.IsZero() {
		first, rowid, err = j.locate(ctx, files, after)
		if err != nil {
			return nil, err
		}
	}
	var out []Observation
	for i := first; i < len(files) && len(out) < limit; i++ {
		from := int64(0)
		if i == first {
			from = rowid
		}
		rows, err := j.readFile(ctx, files[i].Hour, from, limit-len(out))
		if isCorrupt(err) || errors.Is(err, ErrSchemaVersion) {
			return nil, &JournalDamage{Hour: files[i].Hour, Cause: err}
		}
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

// locate finds where to resume after a cursor: the file index and the rowid to read past.
// The anchor is the newest row of the cursor's session with seq <= cursor.Seq, so an
// empty-batch cursor (no exact row) or one beyond every stored seq never replays that
// session's older records. If the session only has later seqs, reading starts at its
// first row. If the session is unknown, reading starts at the oldest file.
func (j *Journal) locate(ctx context.Context, files []JournalFile, after Cursor) (int, int64, error) {
	weakFile, weakRowid, weak := 0, int64(0), false
	for i := len(files) - 1; i >= 0; i-- {
		rowid, exact, found, err := j.findRow(ctx, files[i].Hour, after)
		if isCorrupt(err) || errors.Is(err, ErrSchemaVersion) {
			return 0, 0, &JournalDamage{Hour: files[i].Hour, Cause: err}
		}
		if err != nil {
			return 0, 0, err
		}
		if exact {
			return i, rowid, nil
		}
		if found {
			weakFile, weakRowid, weak = i, rowid, true
		}
	}
	if weak {
		return weakFile, weakRowid, nil
	}
	return 0, 0, nil
}

// findRow looks in one file for the cursor's session. exact reports a row with
// seq <= c.Seq (rowid is that row, the newest such one); otherwise found reports that
// the session has rows in this file, all with larger seqs, and rowid precedes the first.
func (j *Journal) findRow(ctx context.Context, hour time.Time, c Cursor) (rowid int64, exact, found bool, err error) {
	db, err := j.openForRead(ctx, hour)
	if err != nil || db == nil {
		return 0, false, false, err
	}
	defer func() { _ = db.Close() }()
	var anchor sql.NullInt64
	if err = db.QueryRowContext(ctx, "SELECT max(rowid) FROM observations WHERE session=? AND seq<=?", c.Session, int64(c.Seq)).Scan(&anchor); err != nil {
		return 0, false, false, fmt.Errorf("historystore: journal locate: %w", err)
	}
	if anchor.Valid {
		return anchor.Int64, true, true, nil
	}
	if err = db.QueryRowContext(ctx, "SELECT min(rowid)-1 FROM observations WHERE session=?", c.Session).Scan(&anchor); err != nil {
		return 0, false, false, fmt.Errorf("historystore: journal locate: %w", err)
	}
	return anchor.Int64, false, anchor.Valid, nil
}

func (j *Journal) readFile(ctx context.Context, hour time.Time, afterRowid int64, limit int) ([]Observation, error) {
	db, err := j.openForRead(ctx, hour)
	if err != nil || db == nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(ctx, "SELECT session,seq,at_ms,kind,flow_key,body FROM observations WHERE rowid>? ORDER BY rowid LIMIT ?", afterRowid, limit)
	if err != nil {
		return nil, fmt.Errorf("historystore: journal read: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Observation
	for rows.Next() {
		var o Observation
		var seq int64
		if err = rows.Scan(&o.Session, &seq, &o.AtMs, &o.Kind, &o.FlowKey, &o.Body); err != nil {
			return nil, fmt.Errorf("historystore: journal read: %w", err)
		}
		o.Seq = uint64(seq)
		out = append(out, o)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("historystore: journal read: %w", err)
	}
	return out, nil
}

// HourOf returns the hour of the file holding the row ReadAfter would resume from
// for c (see locate). ok is false for the zero cursor and for a cursor that matches
// nothing, so a caller reading "a file earlier than HourOf is fully consumed"
// never treats an unknown position as progress.
func (j *Journal) HourOf(ctx context.Context, c Cursor) (time.Time, bool, error) {
	if c.IsZero() {
		return time.Time{}, false, nil
	}
	if err := c.validate(); err != nil {
		return time.Time{}, false, err
	}
	files, err := j.Files()
	if err != nil {
		return time.Time{}, false, err
	}
	index, rowid, err := j.locate(ctx, files, c)
	if err != nil || len(files) == 0 || index == 0 && rowid == 0 {
		return time.Time{}, false, err
	}
	return files[index].Hour, true, nil
}

// Tail returns the newest appended row, including synthetic poll/gap records. It
// freezes a processing horizon without following records appended during a backlog scan.
func (j *Journal) Tail(ctx context.Context) (Cursor, error) {
	files, err := j.Files()
	if err != nil {
		return Cursor{}, err
	}
	for i := len(files) - 1; i >= 0; i-- {
		db, e := j.openForRead(ctx, files[i].Hour)
		if isCorrupt(e) || errors.Is(e, ErrSchemaVersion) {
			return Cursor{}, &JournalDamage{Hour: files[i].Hour, Cause: e}
		}
		if e != nil {
			return Cursor{}, e
		}
		if db == nil {
			continue
		}
		var c Cursor
		e = db.QueryRowContext(ctx, "SELECT session,seq FROM observations ORDER BY rowid DESC LIMIT 1").Scan(&c.Session, &c.Seq)
		_ = db.Close()
		if errors.Is(e, sql.ErrNoRows) {
			continue
		}
		if isCorrupt(e) || errors.Is(e, ErrSchemaVersion) {
			return Cursor{}, &JournalDamage{Hour: files[i].Hour, Cause: e}
		}
		return c, e
	}
	return Cursor{}, nil
}
