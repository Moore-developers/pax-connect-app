package aiusage

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

const requestColumns = "cli,request_key,at,day,session,segment,profile_id,profile_name,model,streaming,succeeded,error,input,read,write,output,input_price,output_price,multiplier,tps,elapsed,cost,c0,c1,c2,c3,details"
const summaryColumns = "count,success,input,read,write,output,c0,c1,c2,c3,cost,tps_sum,elapsed_sum,tps_count,elapsed_count,b0,b1,b2,b3"

var requestInsert = "INSERT OR REPLACE INTO requests(" + requestColumns + ") VALUES(?" + strings.Repeat(",?", strings.Count(requestColumns, ",")) + ")"

// bucketUpsert adds a Summary to its hour; every column is additive.
var bucketUpsert = func() string {
	var set []string
	for _, c := range strings.Split(summaryColumns, ",") {
		set = append(set, c+"="+c+"+excluded."+c)
	}
	return "INSERT INTO usage_hourly(hour,cli,profile_id,model,profile_name," + summaryColumns + ") VALUES(?,?,?,?,?" + strings.Repeat(",?", strings.Count(summaryColumns, ",")+1) + ") ON CONFLICT(hour,cli,profile_id,model) DO UPDATE SET " + strings.Join(set, ",")
}()

type bucketKey struct {
	At                  int64
	CLI, Profile, Model string
}

// commitRows is the one write path: it stores rows and adds them to the hourly
// buckets in the caller's transaction. A request seen again only ever updates a
// stored Claude row or an explicitly associated Codex model (see refineClaude/refineCodex).
// Evidenced Codex aliases share one counted request; retired markers never restore rows.
// It returns the number of rows written or updated.
func commitRows(ctx context.Context, tx *sql.Tx, rows []Request, attr attributor) (int, bool, error) {
	stmt, e := tx.PrepareContext(ctx, requestInsert)
	if e != nil {
		return 0, false, ErrUnavailable
	}
	defer func() { _ = stmt.Close() }()
	grouped := map[bucketKey]Bucket{}
	inserted := 0
	invalidated := false
	for _, r := range rows {
		if r.analysis != nil {
			n, err := writeAnalysisEvent(ctx, tx, *r.analysis)
			if err != nil {
				return 0, false, err
			}
			inserted += n
			if r.Key == "" {
				continue
			}
		}
		if r.codex != nil {
			n, invalid, err := commitCodex(ctx, tx, stmt, r, grouped, attr)
			if err != nil {
				return 0, false, err
			}
			inserted += n
			invalidated = invalidated || invalid
			continue
		}
		old, exists, e := readRequest(ctx, tx, r.CLI, r.Key)
		if e != nil {
			return 0, false, e
		}
		if exists {
			if next, ok := refineClaude(old, r); ok {
				accumulate(grouped, old, -1)
				if e = writeRequest(ctx, stmt, next); e != nil {
					return 0, false, e
				}
				accumulate(grouped, next, 1)
				inserted++
			}
			continue
		}
		marker, e := tx.ExecContext(ctx, "INSERT OR IGNORE INTO request_keys VALUES(?,?)", keyHash(r.CLI, r.Key), r.At)
		if e != nil {
			return 0, false, ErrUnavailable
		}
		if n, e := marker.RowsAffected(); e != nil {
			return 0, false, ErrUnavailable
		} else if n == 0 {
			continue
		}
		if e = writeRequest(ctx, stmt, r); e != nil {
			return 0, false, e
		}
		accumulate(grouped, r, 1)
		inserted++
	}
	for _, r := range rows {
		if r.Key != "" && r.turn != "" {
			n, err := linkAnalysisRequest(ctx, tx, r)
			if err != nil {
				return 0, false, err
			}
			inserted += n
		}
	}
	if e = writeBuckets(ctx, tx, grouped); e != nil {
		return 0, false, e
	}
	if invalidated {
		if err := pruneEmptyCodexBuckets(ctx, tx, grouped); err != nil {
			return 0, false, err
		}
	}
	return inserted, invalidated, nil
}

func (s *Store) commit(ctx context.Context, rows []Request, c cursor, attribution ...attributor) error {
	tx, e := s.w.BeginTx(ctx, nil)
	if e != nil {
		return ErrUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	var attr attributor
	if len(attribution) > 0 {
		attr = attribution[0]
	}
	inserted, invalidated, e := commitRows(ctx, tx, rows, attr)
	if e != nil {
		return e
	}
	state, e := json.Marshal(c.State)
	if e != nil {
		return ErrUnavailable
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO cursors VALUES(?,?,?,?,?,?) ON CONFLICT(path) DO UPDATE SET inode=excluded.inode,size=excluded.size,mtime=excluded.mtime,offset=excluded.offset,state=excluded.state", c.Path, c.Inode, c.Size, c.Mtime, c.Offset, state); e != nil {
		return ErrUnavailable
	}
	if inserted > 0 {
		if _, e = tx.ExecContext(ctx, "UPDATE meta SET value=value+1 WHERE key='revision'"); e != nil {
			return ErrUnavailable
		}
	}
	if invalidated {
		if _, e = tx.ExecContext(ctx, "UPDATE meta SET value=value+1 WHERE key='generation'"); e != nil {
			return ErrUnavailable
		}
	}
	if e = tx.Commit(); e != nil {
		return ErrUnavailable
	}
	if inserted > 0 {
		s.mutate(func(st *State) {
			st.Revision++
			if invalidated {
				st.Generation++
			}
		})
	}
	return nil
}

func readRequest(ctx context.Context, tx *sql.Tx, cli, key string) (Request, bool, error) {
	rows, e := tx.QueryContext(ctx, "SELECT "+requestColumns+" FROM requests WHERE cli=? AND request_key=?", cli, key)
	if e != nil {
		return Request{}, false, ErrUnavailable
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return Request{}, false, rows.Err()
	}
	r, e := scanRequest(rows)
	return r, true, e
}

func writeRequest(ctx context.Context, stmt *sql.Stmt, r Request) error {
	details, e := json.Marshal(r.Details)
	if e != nil {
		return ErrUnavailable
	}
	_, e = stmt.ExecContext(ctx, r.CLI, r.Key, r.At, time.UnixMilli(r.At).In(time.Local).Format("2006-01-02"), r.Session, r.Segment, r.ProfileID, r.ProfileName, r.Model, r.Streaming, r.Succeeded, r.Error, r.Input, r.Read, r.Write, r.Output, r.InputPrice, r.OutputPrice, r.Multiplier, r.TPS, r.Elapsed, r.Cost, r.Costs[0], r.Costs[1], r.Costs[2], r.Costs[3], details)
	if e != nil {
		return ErrUnavailable
	}
	return nil
}

func accumulate(grouped map[bucketKey]Bucket, r Request, sign int64) {
	key := bucketKey{r.At / 3600000 * 3600000, r.CLI, r.ProfileID, r.Model}
	v := grouped[key]
	v.At, v.CLI, v.ProfileID, v.ProfileName, v.Model = key.At, r.CLI, r.ProfileID, r.ProfileName, r.Model
	sum := summarize(r)
	if sign < 0 {
		sum = sum.negate()
	}
	v.add(sum)
	grouped[key] = v
}

func summaryValues(v Summary) []any {
	return []any{v.Count, v.Success, v.Input, v.Read, v.Write, v.Output, v.Costs[0], v.Costs[1], v.Costs[2], v.Costs[3], v.Cost, v.TPSSum, v.ElapsedSum, v.TPSCount, v.ElapsedCount, v.BilledCosts[0], v.BilledCosts[1], v.BilledCosts[2], v.BilledCosts[3]}
}

func summaryTargets(v *Summary) []any {
	return []any{&v.Count, &v.Success, &v.Input, &v.Read, &v.Write, &v.Output, &v.Costs[0], &v.Costs[1], &v.Costs[2], &v.Costs[3], &v.Cost, &v.TPSSum, &v.ElapsedSum, &v.TPSCount, &v.ElapsedCount, &v.BilledCosts[0], &v.BilledCosts[1], &v.BilledCosts[2], &v.BilledCosts[3]}
}

// writeBuckets adds each Summary to its hour.
func writeBuckets(ctx context.Context, tx *sql.Tx, grouped map[bucketKey]Bucket) error {
	stmt, e := tx.PrepareContext(ctx, bucketUpsert)
	if e != nil {
		return ErrUnavailable
	}
	defer func() { _ = stmt.Close() }()
	for _, b := range grouped {
		if _, e = stmt.ExecContext(ctx, append([]any{b.At, b.CLI, b.ProfileID, b.Model, b.ProfileName}, summaryValues(b.Summary)...)...); e != nil {
			return ErrUnavailable
		}
	}
	return nil
}

func scanRequest(rows *sql.Rows) (Request, error) {
	var r Request
	var day string
	var details []byte
	e := rows.Scan(&r.CLI, &r.Key, &r.At, &day, &r.Session, &r.Segment, &r.ProfileID, &r.ProfileName, &r.Model, &r.Streaming, &r.Succeeded, &r.Error, &r.Input, &r.Read, &r.Write, &r.Output, &r.InputPrice, &r.OutputPrice, &r.Multiplier, &r.TPS, &r.Elapsed, &r.Cost, &r.Costs[0], &r.Costs[1], &r.Costs[2], &r.Costs[3], &details)
	if e == nil {
		e = json.Unmarshal(details, &r.Details)
	}
	return r, e
}

// refineClaude lets a later content block of the same Claude request update the stored
// row: the largest output wins (the last block carries the final count) and a longer
// elapsed replaces a shorter one. Identity and ownership are never touched; cost is
// recomputed with the stored multiplier only when the output grew.
func refineClaude(old, r Request) (Request, bool) {
	if r.CLI != "claude" {
		return old, false
	}
	grew := r.Output > old.Output
	longer := r.Elapsed != nil && (old.Elapsed == nil || *r.Elapsed > *old.Elapsed)
	if !grew && !longer {
		return old, false
	}
	if grew {
		old.Output = r.Output
		old.price()
	}
	if longer {
		old.Elapsed = r.Elapsed
	}
	if old.Elapsed != nil {
		tps := float64(old.Output) / *old.Elapsed
		old.TPS = &tps
	}
	return old, true
}
