package aiusage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"
)

// ErrBusy means the collector kept the database for the whole wait.
var ErrBusy = errors.New("ai_usage_busy")

const overrideColumns = "id,created_at,profile_id,model_id,from_ms,to_ms,all_range,multiplier,models"

// Override is one explicit repricing the user asked for. It outranks the profile switch
// timeline for every request it covers, so a rescan or rebuild cannot undo it. Of several
// overrides covering a request the one created last wins. Covered are requests of
// ProfileID (and Model, when set) with From <= at < To; To never passes the creation time,
// so requests that arrive later follow the timeline again.
type Override struct {
	ID, CreatedAt, From, To int64
	ProfileID, Model        string
	All                     bool
	Multiplier              float64
	Models                  map[string]float64 // per-model values of a whole-profile override
}

func (o Override) covers(profile, model string, at int64) bool {
	return o.ProfileID == profile && (o.Model == "" || o.Model == model) && at >= o.From && at < o.To
}

func (o Override) multiplierFor(model string) float64 {
	if v, ok := o.Models[model]; ok {
		return v
	}
	return o.Multiplier
}

// overrides indexes overrides by Profile, oldest first.
type overrides map[string][]Override

// match returns the latest-created override covering the request.
func (ov overrides) match(profile, model string, at int64) (Override, bool) {
	list := ov[profile]
	for i := len(list) - 1; i >= 0; i-- {
		if list[i].covers(profile, model, at) {
			return list[i], true
		}
	}
	return Override{}, false
}

// attributor decides a request's Profile and multiplier: the timeline, then overrides.
type attributor struct {
	resolve   Resolver
	overrides overrides
}

// attributor loads the overrides once for a scan or rebuild stage.
func (s *Store) attributor(ctx context.Context, resolve Resolver) (attributor, error) {
	ov, e := loadOverrides(ctx, s.r)
	return attributor{resolve, ov}, e
}

func (a attributor) multiplier(profile, model string, at int64, timeline float64) float64 {
	if o, ok := a.overrides.match(profile, model, at); ok {
		return o.multiplierFor(model)
	}
	return timeline
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func loadOverrides(ctx context.Context, db queryer) (overrides, error) {
	rows, e := db.QueryContext(ctx, "SELECT "+overrideColumns+" FROM reprice_overrides ORDER BY id")
	if e != nil {
		return nil, ErrUnavailable
	}
	defer func() { _ = rows.Close() }()
	out := overrides{}
	for rows.Next() {
		var o Override
		var models string
		if e = rows.Scan(&o.ID, &o.CreatedAt, &o.ProfileID, &o.Model, &o.From, &o.To, &o.All, &o.Multiplier, &models); e != nil {
			return nil, ErrUnavailable
		}
		if models != "" && json.Unmarshal([]byte(models), &o.Models) != nil {
			return nil, ErrUnavailable
		}
		out[o.ProfileID] = append(out[o.ProfileID], o)
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	return out, nil
}

// copyOverrides carries the overrides of an older database into this empty one, ids kept.
func (s *Store) copyOverrides(ctx context.Context, old *sql.DB) error {
	var n int
	if e := old.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='reprice_overrides'").Scan(&n); e != nil {
		return ErrUnavailable
	}
	if n == 0 {
		return nil
	}
	ov, e := loadOverrides(ctx, old)
	if e != nil {
		return e
	}
	for _, list := range ov {
		for _, o := range list {
			if e = insertOverride(ctx, s.w, o); e != nil {
				return e
			}
		}
	}
	return nil
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func insertOverride(ctx context.Context, db execer, o Override) error {
	models := ""
	if len(o.Models) > 0 {
		b, e := json.Marshal(o.Models)
		if e != nil {
			return ErrUnavailable
		}
		models = string(b)
	}
	if _, e := db.ExecContext(ctx, "INSERT INTO reprice_overrides("+overrideColumns+") VALUES(?,?,?,?,?,?,?,?,?)", o.ID, o.CreatedAt, o.ProfileID, o.Model, o.From, o.To, o.All, o.Multiplier, models); e != nil {
		return ErrUnavailable
	}
	return nil
}

// RepriceQuery asks to charge a Profile's stored requests at its current multipliers.
// Multiplier and Models are the Profile's present values; the Store does not know Profiles.
type RepriceQuery struct {
	ProfileID, Model string
	// FromDay and ToDay are inclusive local days (YYYY-MM-DD); All replaces both.
	FromDay, ToDay string
	All, DryRun    bool
	Multiplier     float64
	Models         map[string]float64
}

// RepriceResult counts what was (or, for a dry run, would be) repriced. Costs are the
// multiplied cost in micro-USD. Aggregated counts requests that survive only in hourly
// buckets: their cost is rescaled per bucket, not per request.
type RepriceResult struct {
	Requests, Aggregated, OldCost, NewCost int64
	OverrideID                             int64
	Revision                               uint64
}

const dayLayout = "2006-01-02"

func validMultiplier(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 100
}

func (q RepriceQuery) rangeMs() (from, to int64, err error) {
	if q.All {
		if q.FromDay != "" || q.ToDay != "" {
			return 0, 0, ErrRange
		}
		return 0, math.MaxInt64, nil
	}
	a, e1 := time.ParseInLocation(dayLayout, q.FromDay, time.Local)
	b, e2 := time.ParseInLocation(dayLayout, q.ToDay, time.Local)
	if e1 != nil || e2 != nil || b.Before(a) {
		return 0, 0, ErrRange
	}
	return a.UnixMilli(), b.AddDate(0, 0, 1).UnixMilli(), nil
}

func (q RepriceQuery) valid() bool {
	if q.ProfileID == "" || !validMultiplier(q.Multiplier) || len(q.Models) > 1000 {
		return false
	}
	for k, v := range q.Models {
		if k == "" || !validMultiplier(v) {
			return false
		}
	}
	return true
}

// forModel is the multiplier the query charges a model.
func (q RepriceQuery) forModel(model string) float64 {
	if v, ok := q.Models[model]; ok {
		return v
	}
	return q.Multiplier
}

// reprice charges the request at m from its stored unit prices, exactly as price() does
// for the same multiplier. A request without unit prices (unknown model) stays free.
func (r *Request) reprice(m float64) {
	r.Multiplier = m
	r.Cost = 0
	r.Details.Billed = [4]int64{}
	for _, p := range r.Details.Prices {
		if p == nil {
			return
		}
	}
	tokens := [4]int64{max(0, r.Input-r.Read-r.Write), r.Read, r.Write, r.Output}
	var total float64
	for i, p := range r.Details.Prices {
		amount := float64(tokens[i]) * *p
		r.Details.Billed[i] = int64(math.Round(amount * m))
		total += amount
	}
	r.Cost = int64(math.Round(total * m))
}

func (s *Store) lockScan(ctx context.Context) error {
	for !s.scanMu.TryLock() {
		select {
		case <-ctx.Done():
			return ErrBusy
		case <-time.After(20 * time.Millisecond):
		}
	}
	return nil
}

// Reprice applies the query's multipliers to the stored requests of one Profile in one
// transaction, fixes the hourly buckets by the same deltas and records an Override so
// later scans and rebuilds keep the result. A dry run does all of it and rolls back, so
// its numbers are exactly what the real run produces. Requests without a Profile (DSH)
// are never touched. It waits for a running scan, which owns the same write path.
func (s *Store) Reprice(ctx context.Context, q RepriceQuery) (RepriceResult, error) {
	var res RepriceResult
	from, to, e := q.rangeMs()
	if e != nil || !q.valid() {
		return res, ErrRange
	}
	if s.RebuildRequired() {
		return res, ErrUnavailable
	}
	if e = s.lockScan(ctx); e != nil {
		return res, e
	}
	defer s.scanMu.Unlock()
	now := s.options.Now().UnixMilli()
	to = min(to, now+1)
	tx, e := s.w.BeginTx(ctx, nil)
	if e != nil {
		return res, ErrUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	deltas := map[bucketKey]Bucket{}
	if from < to {
		if e = s.repriceRequests(ctx, tx, q, from, to, deltas, &res); e != nil {
			return res, e
		}
		if e = repriceBuckets(ctx, tx, q, from, to, deltas, &res); e != nil {
			return res, e
		}
		if e = writeBuckets(ctx, tx, deltas); e != nil {
			return res, e
		}
	}
	if q.DryRun {
		return res, nil
	}
	if from < to {
		o := Override{CreatedAt: now, ProfileID: q.ProfileID, Model: q.Model, From: from, To: to, All: q.All, Multiplier: q.Multiplier, Models: q.Models}
		if q.Model != "" {
			o.Multiplier, o.Models = q.forModel(q.Model), nil
		}
		var id sql.NullInt64
		if e = tx.QueryRowContext(ctx, "SELECT max(id) FROM reprice_overrides").Scan(&id); e != nil {
			return res, ErrUnavailable
		}
		o.ID = id.Int64 + 1
		if e = insertOverride(ctx, tx, o); e != nil {
			return res, e
		}
		res.OverrideID = o.ID
	}
	changed := res.Requests > 0
	if changed {
		if _, e = tx.ExecContext(ctx, "UPDATE meta SET value=value+1 WHERE key='revision'"); e != nil {
			return res, ErrUnavailable
		}
	}
	if e = tx.Commit(); e != nil {
		return res, ErrUnavailable
	}
	if changed {
		s.mutate(func(st *State) { st.Revision++ })
	}
	res.Revision = s.State().Revision
	return res, nil
}

func addDelta(grouped map[bucketKey]Bucket, k bucketKey, name string, d Summary) {
	v := grouped[k]
	v.At, v.CLI, v.ProfileID, v.ProfileName, v.Model = k.At, k.CLI, k.Profile, name, k.Model
	v.ServiceSource = k.Source
	v.add(d)
	grouped[k] = v
}

// repriceRequests walks the matching stored requests in key order, in bounded batches.
func (s *Store) repriceRequests(ctx context.Context, tx *sql.Tx, q RepriceQuery, from, to int64, grouped map[bucketKey]Bucket, res *RepriceResult) error {
	where := "profile_id=? AND cli<>'dsh' AND at>=? AND at<?"
	args := []any{q.ProfileID, from, to}
	if q.Model != "" {
		where += " AND model=?"
		args = append(args, q.Model)
	}
	cli, key := "", ""
	for {
		rows, e := tx.QueryContext(ctx, "SELECT "+requestColumns+" FROM requests WHERE "+where+" AND (cli,request_key)>(?,?) ORDER BY cli,request_key LIMIT 500", append(append([]any{}, args...), cli, key)...)
		if e != nil {
			return ErrUnavailable
		}
		var batch []Request
		for rows.Next() {
			r, e := scanRequest(rows)
			if e != nil {
				_ = rows.Close()
				return ErrUnavailable
			}
			batch = append(batch, r)
		}
		_ = rows.Close()
		if rows.Err() != nil {
			return ErrUnavailable
		}
		for _, old := range batch {
			next := old
			next.reprice(q.forModel(old.Model))
			res.Requests++
			res.OldCost += old.Cost
			res.NewCost += next.Cost
			if next.Multiplier == old.Multiplier && next.Cost == old.Cost && next.Details.Billed == old.Details.Billed {
				continue
			}
			details, e := json.Marshal(next.Details)
			if e != nil {
				return ErrUnavailable
			}
			if _, e = tx.ExecContext(ctx, "UPDATE requests SET multiplier=?,cost=?,details=? WHERE cli=? AND request_key=?", next.Multiplier, next.Cost, details, old.CLI, old.Key); e != nil {
				return ErrUnavailable
			}
			d := Summary{Cost: next.Cost - old.Cost}
			for i := range d.BilledCosts {
				d.BilledCosts[i] = next.Details.Billed[i] - old.Details.Billed[i]
			}
			addDelta(grouped, bucketKey{old.At / 3600000 * 3600000, old.CLI, old.ProfileID, old.Model, old.Details.ServiceSource}, old.ProfileName, d)
		}
		if len(batch) < 500 {
			return nil
		}
		last := batch[len(batch)-1]
		cli, key = last.CLI, last.Key
	}
}

// repriceBuckets rescales the hours older than any stored request: only their summed
// component costs survive, so the multiplier is applied to each bucket as a whole.
func repriceBuckets(ctx context.Context, tx *sql.Tx, q RepriceQuery, from, to int64, grouped map[bucketKey]Bucket, res *RepriceResult) error {
	var first sql.NullInt64
	if e := tx.QueryRowContext(ctx, "SELECT min(at) FROM requests").Scan(&first); e != nil {
		return ErrUnavailable
	}
	limit := int64(math.MaxInt64)
	if first.Valid {
		limit = first.Int64 / 3600000 * 3600000
	}
	where := "profile_id=? AND cli<>'dsh' AND hour>=? AND hour<? AND hour<?"
	args := []any{q.ProfileID, from, to, limit}
	if q.Model != "" {
		where += " AND model=?"
		args = append(args, q.Model)
	}
	rows, e := tx.QueryContext(ctx, "SELECT hour,cli,profile_id,model,profile_name,service_source,"+summaryColumns+" FROM usage_hourly WHERE "+where, args...)
	if e != nil {
		return ErrUnavailable
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var b Bucket
		if e = rows.Scan(append([]any{&b.At, &b.CLI, &b.ProfileID, &b.Model, &b.ProfileName, &b.ServiceSource}, summaryTargets(&b.Summary)...)...); e != nil {
			return ErrUnavailable
		}
		m := q.forModel(b.Model)
		var raw float64
		d := Summary{}
		for i, c := range b.Costs {
			billed := int64(math.Round(float64(c) * m))
			d.BilledCosts[i] = billed - b.BilledCosts[i]
			raw += float64(c)
		}
		cost := int64(math.Round(raw * m))
		d.Cost = cost - b.Cost
		res.Requests += b.Count
		res.Aggregated += b.Count
		res.OldCost += b.Cost
		res.NewCost += cost
		addDelta(grouped, bucketKey{b.At, b.CLI, b.ProfileID, b.Model, b.ServiceSource}, b.ProfileName, d)
	}
	return rows.Err()
}
