package historystore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"
)

const (
	journalDir     = "journal"
	journalVersion = 1
	hourLayout     = "2006010215"
	metaCursorSess = "cursor_session"
	metaCursorSeq  = "cursor_seq"
)

const journalDDL = `
CREATE TABLE observations(
 session TEXT NOT NULL, seq INTEGER NOT NULL, at_ms INTEGER NOT NULL, kind TEXT NOT NULL,
 flow_key TEXT NOT NULL DEFAULT '', body BLOB NOT NULL, PRIMARY KEY(session, seq));
`

var hourFile = regexp.MustCompile(`^\d{10}\.db$`)

// Observation is one append-only journal record; Body is the observation JSON.
type Observation struct {
	Session string
	Seq     uint64
	AtMs    int64
	Kind    string
	FlowKey string
	Body    []byte
}

// JournalFile describes one hour file; Bytes includes the WAL.
type JournalFile struct {
	Hour  time.Time
	Bytes int64
}

// Dropped reports an hour file that was renamed aside because it was corrupt
// or carried another schema version (HS-R6). The controller records the gap.
type Dropped struct {
	ID          string
	Hour        time.Time
	Quarantined string
	Reason      string
}

// Journal is the append-only collection log: one SQLite file per UTC hour.
// Append, DeleteHour and EvictOver belong to the single capture owner; the
// read methods may run concurrently from other goroutines.
type Journal struct {
	dir   string
	owner uint32

	mu         sync.Mutex // guards writer, writerHour and dropped
	writer     *store
	writerHour time.Time
	dropped    []Dropped
}

// OpenJournal prepares historyDir/journal (0700, owner-checked). Hour files open lazily.
func OpenJournal(historyDir string, owner uint32) (*Journal, error) {
	if err := prepareDirectory(historyDir, owner); err != nil {
		return nil, err
	}
	dir := filepath.Join(historyDir, journalDir)
	if err := prepareDirectory(dir, owner); err != nil {
		return nil, err
	}
	j := &Journal{dir: dir, owner: owner}
	state, err := j.losses()
	if err != nil {
		return nil, err
	}
	if len(state.Pending) == 0 {
		pruneQuarantine(dir, hourFile)
	}
	return j, nil
}

func (j *Journal) path(hour time.Time) string {
	return filepath.Join(j.dir, hour.UTC().Format(hourLayout)+".db")
}

// Append writes batch and cursor to the hour file of at in ONE transaction, so a
// file that holds records always holds the cursor that covers them. Replayed
// (session, seq) records are ignored. An empty batch still advances the cursor.
func (j *Journal) Append(ctx context.Context, at time.Time, batch []Observation, cursor Cursor) error {
	if err := cursor.validate(); err != nil {
		return err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	w, err := j.writerFor(ctx, at.UTC().Truncate(time.Hour))
	if err != nil {
		return err
	}
	return w.update(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, "INSERT OR IGNORE INTO observations(session,seq,at_ms,kind,flow_key,body) VALUES(?,?,?,?,?,?)")
		if err != nil {
			return fmt.Errorf("historystore: journal append: %w", err)
		}
		defer func() { _ = stmt.Close() }()
		for _, o := range batch {
			if o.Seq > math.MaxInt64 {
				return ErrCursorRange
			}
			if _, err = stmt.ExecContext(ctx, o.Session, int64(o.Seq), o.AtMs, o.Kind, o.FlowKey, o.Body); err != nil {
				return fmt.Errorf("historystore: journal append: %w", err)
			}
		}
		if err = setMeta(ctx, tx, metaCursorSess, cursor.Session); err != nil {
			return err
		}
		return setMeta(ctx, tx, metaCursorSeq, strconv.FormatUint(cursor.Seq, 10))
	})
}

// writerFor returns the writer for hour, rotating to a new file when the hour advanced.
// A clock that moves backwards keeps writing to the current file. Caller holds mu.
func (j *Journal) writerFor(ctx context.Context, hour time.Time) (*store, error) {
	if j.writer != nil && !hour.After(j.writerHour) {
		return j.writer, nil
	}
	if err := j.closeWriter(); err != nil {
		return nil, err
	}
	spec := storeSpec{ddl: journalDDL, version: journalVersion}
	path := j.path(hour)
	w, err := tryOpen(ctx, path, j.owner, spec)
	if isCorrupt(err) || errors.Is(err, ErrSchemaVersion) {
		reason := "corrupt"
		if errors.Is(err, ErrSchemaVersion) {
			reason = "schema"
		}
		dropped, e := j.loseLocked(ctx, hour, reason)
		if e != nil {
			return nil, e
		}
		j.dropped = append(j.dropped, dropped)
		w, err = tryOpen(ctx, path, j.owner, spec)
	}
	if err != nil {
		return nil, err
	}
	j.writer, j.writerHour = w, hour
	return w, nil
}

func (j *Journal) closeWriter() error {
	if j.writer == nil {
		return nil
	}
	err := j.writer.Close()
	j.writer = nil
	return err
}

// TakeDropped returns and clears the hour files dropped since the last call.
func (j *Journal) TakeDropped() []Dropped {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := j.dropped
	j.dropped = nil
	return out
}

// Files lists the hour files oldest first.
func (j *Journal) Files() ([]JournalFile, error) {
	entries, err := os.ReadDir(j.dir)
	if err != nil {
		return nil, fmt.Errorf("historystore: journal list: %w", err)
	}
	var files []JournalFile
	for _, e := range entries {
		if !hourFile.MatchString(e.Name()) {
			continue
		}
		hour, err := time.ParseInLocation(hourLayout, e.Name()[:10], time.UTC)
		if err != nil {
			continue
		}
		var size int64
		for _, suffix := range append([]string{""}, sidecars[:1]...) {
			if info, err := os.Lstat(filepath.Join(j.dir, e.Name()+suffix)); err == nil {
				size += info.Size()
			}
		}
		files = append(files, JournalFile{Hour: hour, Bytes: size})
	}
	sort.Slice(files, func(a, b int) bool { return files[a].Hour.Before(files[b].Hour) })
	return files, nil
}

// DeleteHour removes the hour file and its companions; a missing file is not an error.
func (j *Journal) DeleteHour(hour time.Time) error {
	hour = hour.UTC().Truncate(time.Hour)
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.removeLocked(hour)
}

func (j *Journal) removeLocked(hour time.Time) error {
	if j.writer != nil && j.writerHour.Equal(hour) {
		if err := j.closeWriter(); err != nil {
			return err
		}
	}
	path := j.path(hour)
	for _, suffix := range append([]string{""}, sidecars...) {
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("historystore: journal delete: %w", err)
		}
	}
	return nil
}

// EvictOver deletes the oldest hour files until the total is within maxBytes and
// maxFiles (HS-R1); the newest file is never deleted. It returns the evicted hours.
func (j *Journal) EvictOver(maxBytes int64, maxFiles int) ([]time.Time, error) {
	return j.evictOver(context.Background(), maxBytes, maxFiles, nil)
}

// EvictOverConsumed omits loss intents only for hours every consumer has passed.
func (j *Journal) EvictOverConsumed(ctx context.Context, maxBytes int64, maxFiles int, consumed []time.Time) ([]time.Time, error) {
	known := map[int64]bool{}
	for _, hour := range consumed {
		known[hour.Unix()] = true
	}
	return j.evictOver(ctx, maxBytes, maxFiles, known)
}
func (j *Journal) evictOver(ctx context.Context, maxBytes int64, maxFiles int, consumed map[int64]bool) ([]time.Time, error) {
	files, err := j.Files()
	if err != nil {
		return nil, err
	}
	var total int64
	for _, f := range files {
		total += f.Bytes
	}
	var evicted []time.Time
	for len(files) > 1 && (total > maxBytes || len(files) > maxFiles) {
		j.mu.Lock()
		var lossErr error
		if consumed[files[0].Hour.Unix()] {
			lossErr = j.removeLocked(files[0].Hour)
		} else {
			var dropped Dropped
			dropped, lossErr = j.loseLocked(ctx, files[0].Hour, "evicted")
			if lossErr == nil {
				j.dropped = append(j.dropped, dropped)
			}
		}
		j.mu.Unlock()
		if err = lossErr; err != nil {
			return evicted, err
		}
		evicted = append(evicted, files[0].Hour)
		total -= files[0].Bytes
		files = files[1:]
	}
	return evicted, nil
}

// Close closes the current writer.
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.closeWriter()
}
