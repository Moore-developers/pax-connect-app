package aiusage

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/klauspost/compress/zstd"
	"pax-connect/daemon/internal/cadence"
)

// pruneInterval is how often rows past the 93-day retention are deleted.
const pruneInterval = 24 * time.Hour

// stamp is a file's identity as the last ingest saw it; an equal stat needs no database read.
type stamp struct{ inode, size, mtime int64 }

type source struct {
	path, cli string
	info      os.FileInfo
}
type batch struct {
	rows   []Request
	cursor cursor
	reply  chan error
}

// Configure starts a bounded scanner with one serial writer. Close owns cancellation.
func (s *Store) Configure(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil || s.RebuildRequired() {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	work, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	loop := cadence.Loop{
		Name:     "ai_usage",
		Interval: func() time.Duration { return cadence.AIUsage(s.isForeground()) },
		Wake:     []<-chan struct{}{s.wake},
		RunFirst: true,
		Run: func(ctx context.Context) (bool, error) {
			before := s.State()
			err := s.Scan(ctx)
			if err != nil && !errors.Is(err, context.Canceled) {
				s.mutate(func(st *State) { st.Error = "ai_usage_unavailable" })
			}
			return s.State() != before, err
		},
	}
	go func() {
		defer close(s.done)
		loop.Serve(work)
	}()
	return nil
}

func (s *Store) isForeground() bool {
	s.mu.Lock()
	f := s.foreground
	s.mu.Unlock()
	return f != nil && f()
}

func (s *Store) sources() ([]source, map[string]string) {
	roots := map[string]string{"claude": filepath.Join(s.options.Home, ".claude"), "codex": filepath.Join(s.options.Home, ".codex"), "dsh": filepath.Join(s.options.Home, ".dsh")}
	for k, v := range s.options.Roots {
		roots[k] = v
	}
	var out []source
	issues := map[string]string{}
	for cli, root := range roots {
		dirs := []string{"sessions"}
		if cli == "claude" {
			dirs = []string{"projects"}
		}
		if cli == "codex" {
			dirs = append(dirs, "archived_sessions")
		}
		for _, dir := range dirs {
			e := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, e error) error {
				if errors.Is(e, os.ErrNotExist) {
					return nil
				}
				if e != nil {
					return ErrUnavailable
				}
				if d.IsDir() {
					return nil
				}
				if d.Type()&os.ModeSymlink != 0 {
					return nil
				}
				matches := cli == "dsh" && strings.HasSuffix(d.Name(), ".jsonl.zstd") || cli != "dsh" && (strings.HasSuffix(d.Name(), ".jsonl") || cli == "claude" && d.Name() == "sessions-index.json")
				if !matches {
					return nil
				}
				st, e := d.Info()
				if e != nil {
					return ErrUnavailable
				}
				if !st.Mode().IsRegular() {
					return nil
				}
				out = append(out, source{path, cli, st})
				return nil
			})
			if e != nil {
				issues[cli] = "source_unavailable"
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].info.ModTime().After(out[j].info.ModTime()) })
	return out, issues
}

// Scan ingests changed files and waits for their atomic cursor/usage commits.
func (s *Store) Scan(ctx context.Context) (result error) {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	if s.RebuildRequired() {
		return nil
	}
	var resolve Resolver
	if s.options.Timeline != nil {
		var e error
		if resolve, e = s.options.Timeline(ctx); e != nil {
			return e
		}
	}
	attr, e := s.attributor(ctx, resolve)
	if e != nil {
		return e
	}
	files, sourceErrors := s.sources()
	defer func() {
		if err := s.scanCatalog(ctx, files, sourceErrors); err != nil && !errors.Is(err, context.Canceled) {
			s.mutate(func(st *State) { st.CatalogError = "ai_session_catalog_unavailable" })
		}
	}()
	if len(sourceErrors) > 0 {
		return ErrUnavailable
	}
	s.forgetMissing(files)
	s.mu.Lock()
	initial := !s.scanned
	s.mu.Unlock()
	// Backfill and error are status carried by the state value; only stored rows move Revision.
	s.mutate(func(st *State) { st.Backfilling = initial })
	defer func() {
		s.mu.Lock()
		s.scanned = true
		s.mu.Unlock()
		s.mutate(func(st *State) {
			st.Backfilling = false
			if result != nil && !errors.Is(result, context.Canceled) {
				st.Error = "ai_usage_unavailable"
			} else {
				st.Error = ""
			}
		})
	}()
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	writes := make(chan batch, 4)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for b := range writes {
			b.reply <- s.commit(work, b.rows, b.cursor, attr)
		}
	}()
	jobs := make(chan source)
	errs := make(chan error, 4)
	var workers sync.WaitGroup
	count := min(runtime.NumCPU(), 4)
	if !initial {
		count = 1
	}
	for range count {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for f := range jobs {
				if e := s.ingest(work, f, attr, writes); e != nil {
					select {
					case errs <- e:
					default:
					}
					cancel()
				}
			}
		}()
	}
dispatch:
	for _, f := range files {
		// Active compressed logs are safe for metadata's complete-frame reader;
		// the established accounting stability delay remains unchanged.
		if filepath.Base(f.path) == "sessions-index.json" || (f.cli == "dsh" && s.options.Now().Sub(f.info.ModTime()) < 30*time.Second) {
			continue
		}
		select {
		case jobs <- f:
		case <-work.Done():
			break dispatch
		}
	}
	close(jobs)
	workers.Wait()
	close(writes)
	<-writerDone
	select {
	case e := <-errs:
		return e
	default:
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return s.prune(ctx)
}

// prune deletes rows past retention at most once a day. Deleting rows advances the
// generation and, because queries only carry a revision, the revision with it (R3, R5), in
// the same transaction as the delete.
func (s *Store) prune(ctx context.Context) error {
	now := s.options.Now()
	if !s.lastPrune.IsZero() && now.Sub(s.lastPrune) < pruneInterval {
		return nil
	}
	tx, e := s.w.BeginTx(ctx, nil)
	if e != nil {
		return ErrUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	res, e := tx.ExecContext(ctx, "DELETE FROM requests WHERE day<?", now.AddDate(0, 0, -93).Format("2006-01-02"))
	if e != nil {
		return ErrUnavailable
	}
	n, _ := res.RowsAffected()
	if res, e = tx.ExecContext(ctx, "DELETE FROM analysis_turns WHERE at<?", now.AddDate(0, 0, -93).UnixMilli()); e != nil {
		return ErrUnavailable
	}
	if removed, err := res.RowsAffected(); err == nil {
		n += removed
	}
	if res, e = tx.ExecContext(ctx, "DELETE FROM analysis_requests WHERE NOT EXISTS(SELECT 1 FROM requests r WHERE r.cli=analysis_requests.cli AND r.request_key=analysis_requests.request_key)"); e != nil {
		return ErrUnavailable
	}
	if removed, err := res.RowsAffected(); err == nil {
		n += removed
	}
	for _, query := range []string{`DELETE FROM session_agent_days WHERE day<?`, `DELETE FROM session_agent_requests WHERE NOT EXISTS(SELECT 1 FROM requests r WHERE r.cli=session_agent_requests.cli AND r.request_key=session_agent_requests.request_key)`} {
		var args []any
		if strings.Contains(query, "day<?") {
			args = []any{now.AddDate(0, 0, -93).Format("2006-01-02")}
		}
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return ErrUnavailable
		}
		removed, _ := result.RowsAffected()
		if removed > 0 {
			if _, err = tx.ExecContext(ctx, `UPDATE meta SET value=value+1 WHERE key='catalog_revision'`); err != nil {
				return ErrUnavailable
			}
		}
	}
	if n > 0 {
		if _, e = tx.ExecContext(ctx, "UPDATE meta SET value=value+1 WHERE key IN('revision','generation')"); e != nil {
			return ErrUnavailable
		}
	}
	var catalogRevision uint64
	if e = tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='catalog_revision'`).Scan(&catalogRevision); e != nil {
		return ErrUnavailable
	}
	if e = tx.Commit(); e != nil {
		return ErrUnavailable
	}
	s.mutate(func(st *State) { st.CatalogRevision = catalogRevision })
	s.lastPrune = now
	if n > 0 {
		s.mutate(func(st *State) { st.Revision++; st.Generation++ })
	}
	return nil
}
func (s *Store) ingest(ctx context.Context, f source, attr attributor, writes chan<- batch) error {
	stat, ok := f.info.Sys().(*syscall.Stat_t)
	if !ok {
		return ErrUnavailable
	}
	inode := int64(stat.Ino)
	seen := stamp{inode, f.info.Size(), f.info.ModTime().UnixNano()}
	if s.stampOf(f.path) == seen {
		return nil
	}
	c, e := s.cursor(ctx, f.path)
	if e != nil {
		return e
	}
	if c.State.AnalysisVersion == 0 {
		c = cursor{Path: f.path}
		c.State.AnalysisVersion = 1
	}
	upgradeCodex := f.cli == "codex" && c.State.CodexOwnerVersion == 0
	if !upgradeCodex && !c.State.CodexPending && c.Inode == inode && c.Size == f.info.Size() && c.Mtime == f.info.ModTime().UnixNano() {
		s.setStamp(f.path, seen)
		return nil
	}
	if c.Inode != inode || c.Offset > f.info.Size() {
		c = cursor{Path: f.path}
		c.State.AnalysisVersion = 1
	}
	c.Inode = inode
	c.Size = f.info.Size()
	c.Mtime = f.info.ModTime().UnixNano()
	file, e := os.OpenFile(f.path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return ErrUnavailable
	}
	defer func() { _ = file.Close() }()
	opened, e := file.Stat()
	if e != nil || !opened.Mode().IsRegular() {
		return ErrUnavailable
	}
	if upgradeCodex {
		if e = prepareCodexCursor(file, &c); e != nil {
			return e
		}
	}
	if f.cli == "codex" {
		c.State.CodexPending = true
	}
	if _, e = file.Seek(c.Offset, io.SeekStart); e != nil {
		return ErrUnavailable
	}
	reader := bufio.NewReaderSize(file, 64*1024)
	var decoder *zstd.Decoder
	if f.cli == "dsh" {
		decoder, e = zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(64<<20))
		if e != nil {
			return ErrUnavailable
		}
		defer decoder.Close()
	}
	var rows []Request
	lines := 0
	offset := c.Offset
	var pending []byte
	flush := func() error {
		for len(rows) > 500 {
			b := batch{append([]Request(nil), rows[:500]...), cursor{Path: c.Path, Inode: c.Inode}, make(chan error, 1)}
			select {
			case writes <- b:
			case <-ctx.Done():
				return ctx.Err()
			}
			select {
			case e := <-b.reply:
				if e != nil {
					return e
				}
			case <-ctx.Done():
				return ctx.Err()
			}
			rows = rows[500:]
		}
		b := batch{append([]Request(nil), rows...), c, make(chan error, 1)}
		select {
		case writes <- b:
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case e := <-b.reply:
			rows = nil
			lines = 0
			return e
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var line []byte
		if decoder == nil {
			line, e = reader.ReadBytes('\n')
			offset += int64(len(line))
			if e != nil {
				break
			}
		} else {
			var raw []byte
			raw, e = readFrame(reader)
			if e != nil {
				break
			}
			offset += int64(len(raw))
			var body []byte
			body, e = decoder.DecodeAll(raw, nil)
			if e != nil {
				return ErrUnavailable
			}
			pending = append(pending, body...)
			if len(pending) > 32<<20 {
				return ErrUnavailable
			}
			if len(pending) == 0 || pending[len(pending)-1] != '\n' {
				continue
			}
			line = pending
			pending = nil
		}
		for _, one := range bytes.Split(line, []byte{'\n'}) {
			if len(one) == 0 {
				continue
			}
			r, skipped := c.State.parse(f.cli, one)
			if c.State.CodexIdentityError {
				return ErrUnavailable
			}
			if skipped {
				s.mutate(func(st *State) { st.Skipped++ })
			}
			if r != nil {
				if r.Key != "" && (r.codex == nil || !r.codex.Refine) {
					r.attribute(attr)
				}
				rows = append(rows, *r)
			}
			lines++
		}
		c.Offset = offset
		if lines >= 500 {
			if e = flush(); e != nil {
				return e
			}
		}
	}
	if e != io.EOF && e != io.ErrUnexpectedEOF {
		return ErrUnavailable
	}
	c.State.CodexPending = false
	if e = flush(); e != nil {
		return e
	}
	s.setStamp(f.path, seen)
	return nil
}

func (s *Store) stampOf(path string) stamp {
	s.stampMu.Lock()
	defer s.stampMu.Unlock()
	return s.stamps[path]
}

func (s *Store) setStamp(path string, v stamp) {
	s.stampMu.Lock()
	s.stamps[path] = v
	s.stampMu.Unlock()
}

// forgetMissing drops stamps of files that left the scan, keeping the map bounded.
func (s *Store) forgetMissing(files []source) {
	live := make(map[string]struct{}, len(files))
	for _, f := range files {
		live[f.path] = struct{}{}
	}
	s.stampMu.Lock()
	for p := range s.stamps {
		if _, ok := live[p]; !ok {
			delete(s.stamps, p)
		}
	}
	s.stampMu.Unlock()
}
