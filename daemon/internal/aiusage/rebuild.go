package aiusage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// RebuildOptions drives the one-off rebuild of ai_usage.db. It is the only way to
// reprocess history: the same pipeline the daemon runs rescans every log, and rows whose
// log is gone are carried over and repriced. The daemon must not be running.
type RebuildOptions struct {
	Directory, Home string
	Roots           map[string]string
	Owner           uint32
	Now             func() time.Time
	DryRun          bool
	// Timeline runs after the backup. earliest holds the first retained data time per
	// CLI. It seeds the profile switch history (only when !dryRun) and returns the
	// resolver the rebuild attributes with, plus one note per seeded event.
	Timeline func(ctx context.Context, earliest map[string]time.Time, dryRun bool) (Resolver, []string, error)
}

// CLIReport counts what one CLI ended up with.
type CLIReport struct {
	Requests, Tokens, CostMicro int64
}

// ModelReport is a model the price table does not cover; its requests cost 0.
type ModelReport struct {
	CLI, Model string
	Requests   int64
}

// Report is what a rebuild did and whether its result reconciles.
type Report struct {
	DryRun, Replaced, Reconciled bool
	Backup                       string
	Seeded                       []string
	Details                      map[string]CLIReport // retained rows
	Lifetime                     map[string]CLIReport // all hourly buckets
	UnknownModels                []ModelReport
	Carried                      map[string]int // old rows whose log is gone, per CLI
	OldBuckets, DroppedBuckets   int            // buckets older than the window: kept / rescan-only dropped
	WindowFrom                   time.Time
	Mismatches                   []string
	QuickCheck                   string
}

func ageCutoff(now time.Time) (day string, windowMs int64) {
	t := now.AddDate(0, 0, -93)
	midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	ms := midnight.UnixMilli()
	return midnight.Format("2006-01-02"), (ms + 3599999) / 3600000 * 3600000
}

// Rebuild replaces ai_usage.db with one computed under the current rules.
func Rebuild(ctx context.Context, o RebuildOptions) (rep Report, err error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	rep = Report{DryRun: o.DryRun, Carried: map[string]int{}}
	if o.Timeline == nil {
		return rep, errors.New("rebuild needs a profile timeline")
	}
	stamp := o.Now().Format("20060102-150405")
	live := filepath.Join(o.Directory, "ai_usage.db")
	_, statErr := os.Stat(live)
	hasOld := statErr == nil
	// The rebuilt file must be renamed onto the live one, so a real run works beside it;
	// a dry run never writes into the data directory, not even temporarily.
	work := filepath.Join(o.Directory, ".ai-usage-rebuild-"+stamp)
	if o.DryRun {
		tmp, e := os.MkdirTemp("", "ai-usage-rebuild-")
		if e == nil {
			work, e = filepath.EvalSymlinks(tmp)
		}
		if e != nil {
			return rep, fmt.Errorf("cannot create work directory: %w", e)
		}
	} else if err = os.Mkdir(work, 0700); err != nil {
		return rep, fmt.Errorf("cannot create work directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(work) }()

	var old *sql.DB
	earliest := map[string]time.Time{}
	if hasOld {
		from := live
		if !o.DryRun {
			rep.Backup = filepath.Join(o.Directory, "backups", "ai-usage-"+stamp)
			if err = copyDatabase(live, rep.Backup, "ai_usage.db"); err != nil {
				return rep, fmt.Errorf("backup failed: %w", err)
			}
			from = filepath.Join(rep.Backup, "ai_usage.db")
		}
		if err = copyDatabase(from, filepath.Join(work, "old"), "ai_usage.db"); err != nil {
			return rep, fmt.Errorf("cannot read the old database: %w", err)
		}
		if old, err = openOld(ctx, filepath.Join(work, "old", "ai_usage.db")); err != nil {
			return rep, err
		}
		if old != nil {
			defer func() { _ = old.Close() }()
			if earliest, err = earliestData(ctx, old); err != nil {
				return rep, err
			}
		}
	}
	resolve, seeded, err := o.Timeline(ctx, earliest, o.DryRun)
	if err != nil {
		return rep, err
	}
	rep.Seeded = seeded
	if resolve == nil {
		resolve = func(string, time.Time) Attribution { return Attribution{Multiplier: 1} }
	}

	newDir := filepath.Join(work, "new")
	s, err := Open(ctx, Options{Directory: newDir, Home: o.Home, Roots: o.Roots, Owner: o.Owner, Now: o.Now, Timeline: func(context.Context) (Resolver, error) { return resolve, nil }})
	if err != nil {
		return rep, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = s.Close(context.Background())
		}
	}()
	if old != nil {
		if err = s.copyRetiredKeys(ctx, old); err != nil {
			return rep, err
		}
		if err = s.copyOverrides(ctx, old); err != nil {
			return rep, err
		}
	}
	if err = s.Scan(ctx); err != nil {
		return rep, fmt.Errorf("log rescan failed: %w", err)
	}
	attr, err := s.attributor(ctx, resolve)
	if err != nil {
		return rep, err
	}
	cutoffDay, window := ageCutoff(o.Now())
	rep.WindowFrom = time.UnixMilli(window)
	if old != nil {
		if err = s.carryOver(ctx, old, attr, cutoffDay, &rep); err != nil {
			return rep, err
		}
	}
	if rep.Mismatches, err = s.reconcile(ctx, window); err != nil {
		return rep, err
	}
	rep.Reconciled = len(rep.Mismatches) == 0
	if old != nil && rep.Reconciled {
		if err = s.keepOlderBuckets(ctx, old, attr, window, &rep); err != nil {
			return rep, err
		}
	}
	if err = s.summarizeInto(ctx, &rep); err != nil {
		return rep, err
	}
	if old != nil {
		if err = s.carryMeta(ctx, old); err != nil {
			return rep, err
		}
	}
	if err = s.w.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&rep.QuickCheck); err != nil {
		return rep, ErrUnavailable
	}
	closed = true
	if err = s.Close(ctx); err != nil {
		return rep, err
	}
	if !rep.Reconciled || rep.QuickCheck != "ok" {
		return rep, errors.New("the rebuilt database did not reconcile; nothing was replaced")
	}
	if err = singleFile(ctx, filepath.Join(newDir, "ai_usage.db")); err != nil {
		return rep, err
	}
	if o.DryRun {
		return rep, nil
	}
	return rep, replaceDatabase(filepath.Join(newDir, "ai_usage.db"), live, &rep)
}

// carryOver keeps rows whose source log no longer exists: tokens and time stay, while
// ownership and price are recomputed under the current rules.
func (s *Store) carryOver(ctx context.Context, old *sql.DB, attr attributor, cutoffDay string, rep *Report) error {
	rows, err := old.QueryContext(ctx, "SELECT cli,request_key,at,session,segment,model,streaming,succeeded,error,input,read,write,output,tps,elapsed,details FROM requests ORDER BY at")
	if err != nil {
		return fmt.Errorf("cannot read old requests: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var batch []Request
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		tx, e := s.w.BeginTx(ctx, nil)
		if e != nil {
			return ErrUnavailable
		}
		defer func() { _ = tx.Rollback() }()
		if _, _, e = commitRows(ctx, tx, batch, attr); e != nil {
			return e
		}
		for _, r := range batch {
			if r.CLI == "codex" {
				if e = copyCarriedCodexEvidence(ctx, old, tx, r.Key); e != nil {
					return e
				}
			}
		}
		batch = batch[:0]
		if e = tx.Commit(); e != nil {
			return ErrUnavailable
		}
		return nil
	}
	for rows.Next() {
		r, e := scanOld(rows)
		if e != nil {
			return fmt.Errorf("cannot read an old request: %w", e)
		}
		if time.UnixMilli(r.At).In(time.Local).Format("2006-01-02") < cutoffDay {
			continue
		}
		if seen, e := s.hasKey(ctx, r.CLI, r.Key); e != nil {
			return e
		} else if seen {
			continue
		}
		r.attribute(attr)
		batch = append(batch, r)
		rep.Carried[r.CLI]++
		if len(batch) >= 500 {
			if e = flush(); e != nil {
				return e
			}
		}
	}
	if err = rows.Err(); err != nil {
		return ErrUnavailable
	}
	return flush()
}

func (s *Store) hasKey(ctx context.Context, cli, key string) (bool, error) {
	var n int
	if e := s.r.QueryRowContext(ctx, "SELECT count(*) FROM request_keys WHERE key_hash=?", keyHash(cli, key)).Scan(&n); e != nil {
		return false, ErrUnavailable
	}
	return n > 0, nil
}

// reconcile checks that every hourly bucket in the retained window equals the sum of
// its stored requests, by feeding the rows through the pipeline's own accumulator.
func (s *Store) reconcile(ctx context.Context, window int64) ([]string, error) {
	want := map[bucketKey]Bucket{}
	rows, err := s.r.QueryContext(ctx, "SELECT "+requestColumns+" FROM requests WHERE at>=?", window)
	if err != nil {
		return nil, ErrUnavailable
	}
	for rows.Next() {
		r, e := scanRequest(rows)
		if e != nil {
			_ = rows.Close()
			return nil, ErrUnavailable
		}
		accumulate(want, r, 1)
	}
	_ = rows.Close()
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	got := map[bucketKey]Bucket{}
	brows, err := s.r.QueryContext(ctx, "SELECT hour,cli,profile_id,model,profile_name,"+summaryColumns+" FROM usage_hourly WHERE hour>=?", window)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer func() { _ = brows.Close() }()
	for brows.Next() {
		var b Bucket
		if e := brows.Scan(append([]any{&b.At, &b.CLI, &b.ProfileID, &b.Model, &b.ProfileName}, summaryTargets(&b.Summary)...)...); e != nil {
			return nil, ErrUnavailable
		}
		got[bucketKey{b.At, b.CLI, b.ProfileID, b.Model}] = b
	}
	var out []string
	for k, w := range want {
		if g, ok := got[k]; !ok || !sameSummary(g.Summary, w.Summary) {
			out = append(out, fmt.Sprintf("hour %s %s %s: buckets %+v, requests %+v", time.UnixMilli(k.At).UTC().Format(time.RFC3339), k.CLI, k.Model, got[k].Summary, w.Summary))
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			out = append(out, fmt.Sprintf("hour %s %s %s: a bucket without requests", time.UnixMilli(k.At).UTC().Format(time.RFC3339), k.CLI, k.Model))
		}
	}
	sort.Strings(out)
	if len(out) > 10 {
		out = append(out[:10], fmt.Sprintf("... %d more", len(out)-10))
	}
	return out, brows.Err()
}

func sameSummary(a, b Summary) bool {
	fa, fb := a, b
	fa.TPSSum, fa.ElapsedSum, fb.TPSSum, fb.ElapsedSum = 0, 0, 0, 0
	return fa == fb && math.Abs(a.TPSSum-b.TPSSum) < 1e-6 && math.Abs(a.ElapsedSum-b.ElapsedSum) < 1e-6
}

// keepOlderBuckets swaps the rescan's buckets before the window for the old database's:
// those hours have no retained requests, so only their multiplier and Profile are
// recomputed. Component prices cannot be rederived from an hourly sum, so costs that the
// old rules left unpriced stay as they were.
func (s *Store) keepOlderBuckets(ctx context.Context, old *sql.DB, attr attributor, window int64, rep *Report) error {
	rows, err := old.QueryContext(ctx, "SELECT hour,cli,profile_id,model,profile_name,"+summaryColumns+" FROM usage_hourly WHERE hour<?", window)
	if err != nil {
		return fmt.Errorf("cannot read old buckets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, "DELETE FROM usage_hourly WHERE hour<?", window)
	if err != nil {
		return ErrUnavailable
	}
	dropped, _ := res.RowsAffected()
	rep.DroppedBuckets = int(dropped)
	grouped := map[bucketKey]Bucket{}
	for rows.Next() {
		var b Bucket
		if e := rows.Scan(append([]any{&b.At, &b.CLI, &b.ProfileID, &b.Model, &b.ProfileName}, summaryTargets(&b.Summary)...)...); e != nil {
			return ErrUnavailable
		}
		a := Attribution{Multiplier: 1}
		if b.CLI != "dsh" {
			a = attr.resolve(b.CLI, time.UnixMilli(b.At))
		}
		b.ProfileID, b.ProfileName = a.ProfileID, a.ProfileName
		m := a.ForModel(b.Model)
		if b.CLI != "dsh" && a.ProfileID != "" {
			m = attr.multiplier(a.ProfileID, b.Model, b.At, m)
		}
		var raw float64
		for i, c := range b.Costs {
			b.BilledCosts[i] = int64(math.Round(float64(c) * m))
			raw += float64(c)
		}
		b.Cost = int64(math.Round(raw * m))
		k := bucketKey{b.At, b.CLI, b.ProfileID, b.Model}
		v := grouped[k]
		v.At, v.CLI, v.ProfileID, v.ProfileName, v.Model = b.At, b.CLI, b.ProfileID, b.ProfileName, b.Model
		v.add(b.Summary)
		grouped[k] = v
		rep.OldBuckets++
	}
	if rows.Err() != nil {
		return ErrUnavailable
	}
	if err = writeBuckets(ctx, tx, grouped); err != nil {
		return err
	}
	return tx.Commit()
}

func knownModel(model string) bool {
	_, ok := tariffs[model]
	return ok || model == syntheticModel || model == "deepseek-flash" || model == "deepseek-v4.1-flash"
}

func (s *Store) summarizeInto(ctx context.Context, rep *Report) error {
	rep.Details, rep.Lifetime = map[string]CLIReport{}, map[string]CLIReport{}
	for _, q := range []struct {
		into  map[string]CLIReport
		query string
	}{
		{rep.Details, "SELECT cli,count(*),sum(input+output),sum(cost) FROM requests GROUP BY cli"},
		{rep.Lifetime, "SELECT cli,sum(count),sum(input+output),sum(cost) FROM usage_hourly GROUP BY cli"},
	} {
		rows, e := s.r.QueryContext(ctx, q.query)
		if e != nil {
			return ErrUnavailable
		}
		for rows.Next() {
			var cli string
			var c CLIReport
			if e = rows.Scan(&cli, &c.Requests, &c.Tokens, &c.CostMicro); e != nil {
				_ = rows.Close()
				return ErrUnavailable
			}
			q.into[cli] = c
		}
		_ = rows.Close()
		if rows.Err() != nil {
			return ErrUnavailable
		}
	}
	rows, e := s.r.QueryContext(ctx, "SELECT cli,model,sum(count) FROM usage_hourly GROUP BY cli,model ORDER BY cli,model")
	if e != nil {
		return ErrUnavailable
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var m ModelReport
		if e = rows.Scan(&m.CLI, &m.Model, &m.Requests); e != nil {
			return ErrUnavailable
		}
		if !knownModel(m.Model) {
			rep.UnknownModels = append(rep.UnknownModels, m)
		}
	}
	return rows.Err()
}

// singleFile folds the WAL into the database so one file is all that moves.
func singleFile(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = db.Close() }()
	var mode string
	if err = db.QueryRowContext(ctx, "PRAGMA journal_mode=DELETE").Scan(&mode); err != nil || mode != "delete" {
		return ErrUnavailable
	}
	return nil
}

// replaceDatabase swaps the rebuilt file in. The old WAL and shm go first: they belong to
// the old file and would corrupt the new one. The backup holds all three.
func replaceDatabase(built, live string, rep *Report) error {
	if _, err := os.Stat(filepath.Join(rep.Backup, "ai_usage.db")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if err := os.Remove(live + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(built, live); err != nil {
		return err
	}
	rep.Replaced = true
	if d, err := os.Open(filepath.Dir(live)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// copyDatabase copies a SQLite file with its WAL and shm into a new private directory.
func copyDatabase(from, dir, name string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		src, err := os.Open(from + suffix)
		if errors.Is(err, os.ErrNotExist) && suffix != "" {
			continue
		}
		if err != nil {
			return err
		}
		dst, err := os.OpenFile(filepath.Join(dir, name+suffix), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			_ = src.Close()
			return err
		}
		_, err = io.Copy(dst, src)
		if err == nil {
			err = dst.Sync()
		}
		_ = src.Close()
		if cerr := dst.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// carryMeta continues the old database's version numbers. The rebuilt file holds different
// rows, so the generation advances and the revision with it (R3, R5); neither goes back.
// An old schema without these keys counts as zero.
func (s *Store) carryMeta(ctx context.Context, old *sql.DB) error {
	var revision, generation uint64
	_ = old.QueryRowContext(ctx, "SELECT coalesce((SELECT value FROM meta WHERE key='revision'),0),coalesce((SELECT value FROM meta WHERE key='generation'),0)").Scan(&revision, &generation)
	s.mu.Lock()
	revision = max(revision, s.state.Revision) + 1
	generation++
	s.mu.Unlock()
	for key, value := range map[string]uint64{"revision": revision, "generation": generation} {
		if _, err := s.w.ExecContext(ctx, "UPDATE meta SET value=? WHERE key=?", value, key); err != nil {
			return ErrUnavailable
		}
	}
	return nil
}
