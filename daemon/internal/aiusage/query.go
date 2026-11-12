package aiusage

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// MaxQuerySpanMillis covers a leap calendar year plus local-time offset changes.
const MaxQuerySpanMillis int64 = 367 * 24 * 60 * 60 * 1000

// Buckets returns aggregates only, never request metadata or source lines.
func (s *Store) Buckets(ctx context.Context, q Query) ([]Bucket, error) {
	if q.To <= q.From || q.Granularity != "hour" && q.Granularity != "day" {
		return nil, ErrRange
	}
	if s.RebuildRequired() {
		return nil, nil
	}
	rows, e := s.r.QueryContext(ctx, "SELECT hour,cli,profile_id,model,profile_name,"+summaryColumns+" FROM usage_hourly WHERE hour>=? AND hour<? ORDER BY hour", q.From, q.To)
	if e != nil {
		return nil, ErrUnavailable
	}
	defer func() { _ = rows.Close() }()
	grouped := map[bucketKey]Bucket{}
	for rows.Next() {
		var b Bucket
		targets := []any{&b.At, &b.CLI, &b.ProfileID, &b.Model, &b.ProfileName}
		targets = append(targets, summaryTargets(&b.Summary)...)
		if e = rows.Scan(targets...); e != nil {
			return nil, ErrUnavailable
		}
		if q.Granularity == "day" {
			t := time.UnixMilli(b.At).In(time.Local)
			b.At = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local).UnixMilli()
		}
		k := bucketKey{b.At, b.CLI, b.ProfileID, b.Model}
		v := grouped[k]
		v.At = b.At
		v.CLI = b.CLI
		v.ProfileID = b.ProfileID
		v.ProfileName = b.ProfileName
		v.Model = b.Model
		v.add(b.Summary)
		grouped[k] = v
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	out := make([]Bucket, 0, len(grouped))
	for _, b := range grouped {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].At != out[j].At {
			return out[i].At < out[j].At
		}
		return out[i].CLI+out[i].ProfileID+out[i].Model < out[j].CLI+out[j].ProfileID+out[j].Model
	})
	return out, nil
}

// LastUsed maps each client to its latest request time: the newest retained request,
// or the start hour of its newest bucket once the requests are past retention.
func (s *Store) LastUsed(ctx context.Context) (map[string]int64, error) {
	out := map[string]int64{}
	if s.RebuildRequired() {
		return out, nil
	}
	for _, q := range []string{"SELECT cli,max(hour) FROM usage_hourly WHERE count>0 GROUP BY cli", "SELECT cli,max(at) FROM requests GROUP BY cli"} {
		rows, e := s.r.QueryContext(ctx, q)
		if e != nil {
			return nil, ErrUnavailable
		}
		for rows.Next() {
			var cli string
			var at int64
			if e = rows.Scan(&cli, &at); e != nil {
				_ = rows.Close()
				return nil, ErrUnavailable
			}
			out[cli] = at // the second query overrides: a request time is more exact than its hour
		}
		e = rows.Err()
		_ = rows.Close()
		if e != nil {
			return nil, ErrUnavailable
		}
	}
	return out, nil
}

// Summarize returns lifetime Profile/model totals from retained hourly aggregates.
func (s *Store) Summarize(ctx context.Context) ([]Bucket, error) {
	rows, e := s.Buckets(ctx, Query{From: 0, To: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli(), Granularity: "day"})
	if e != nil {
		return nil, e
	}
	grouped := map[bucketKey]Bucket{}
	for _, b := range rows {
		k := bucketKey{0, b.CLI, b.ProfileID, b.Model}
		v := grouped[k]
		v.CLI = b.CLI
		v.ProfileID = b.ProfileID
		v.ProfileName = b.ProfileName
		v.Model = b.Model
		v.add(b.Summary)
		grouped[k] = v
	}
	out := make([]Bucket, 0, len(grouped))
	for _, v := range grouped {
		out = append(out, v)
	}
	return out, nil
}

// List returns ten records; result tabs exclude their own result filter.
func (s *Store) List(ctx context.Context, q Query) (Logs, error) {
	out := Logs{Requests: []Request{}}
	if q.To <= q.From || q.Page < 0 || len(q.Text) > 256 ||
		(q.MinOfficialCost != nil && *q.MinOfficialCost < 0) ||
		(q.MaxOfficialCost != nil && *q.MaxOfficialCost < 0) ||
		(q.MinOfficialCost != nil && q.MaxOfficialCost != nil && *q.MinOfficialCost > *q.MaxOfficialCost) {
		return out, ErrRange
	}
	if s.RebuildRequired() {
		return out, nil
	}
	where := "at>=? AND at<?"
	args := []any{q.From, q.To}
	profile := q.Profile
	if profile == "__unassigned__" {
		where += " AND profile_id='' AND cli<>'dsh'"
		profile = ""
	}
	if profile == "__dsh__" {
		where += " AND profile_id='' AND cli='dsh'"
		profile = ""
	}
	for _, f := range []struct{ column, value string }{{"cli", q.CLI}, {"profile_id", profile}, {"model", q.Model}} {
		if f.value != "" {
			where += " AND " + f.column + "=?"
			args = append(args, f.value)
		}
	}
	if q.Text != "" {
		where += " AND (request_key LIKE ? ESCAPE '\\' OR model LIKE ? ESCAPE '\\' OR profile_name LIKE ? ESCAPE '\\' OR error LIKE ? ESCAPE '\\')"
		v := "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(q.Text) + "%"
		args = append(args, v, v, v, v)
	}

	if q.MinOfficialCost != nil {
		where += " AND (c0+c1+c2+c3)>=?"
		args = append(args, *q.MinOfficialCost)
	}
	if q.MaxOfficialCost != nil {
		where += " AND (c0+c1+c2+c3)<=?"
		args = append(args, *q.MaxOfficialCost)
	}

	tx, e := s.r.BeginTx(ctx, nil)
	if e != nil {
		return out, ErrUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	if e = tx.QueryRowContext(ctx, "SELECT count(*),coalesce(sum(succeeded),0) FROM requests WHERE "+where, args...).Scan(&out.All, &out.Success); e != nil {
		return out, ErrUnavailable
	}
	out.Failed = out.All - out.Success
	if q.Result != nil {
		where += " AND succeeded=?"
		args = append(args, *q.Result)
	}
	expressions := []string{"count(*)", "coalesce(sum(succeeded),0)"}
	for _, column := range []string{"input", "read", "write", "output", "c0", "c1", "c2", "c3", "cost"} {
		expressions = append(expressions, "coalesce(sum("+column+"),0)")
	}
	for _, column := range []string{"tps", "elapsed"} {
		expressions = append(expressions, "coalesce(sum(CASE WHEN succeeded=1 THEN "+column+" END),0)")
	}
	for _, column := range []string{"tps", "elapsed"} {
		expressions = append(expressions, "count(CASE WHEN succeeded=1 THEN "+column+" END)")
	}
	for i := range 4 {
		expressions = append(expressions, fmt.Sprintf("coalesce(sum(json_extract(details,'$.Billed[%d]')),0)", i))
	}
	if e = tx.QueryRowContext(ctx, "SELECT "+strings.Join(expressions, ",")+" FROM requests WHERE "+where, args...).Scan(summaryTargets(&out.Total)...); e != nil {
		return out, ErrUnavailable
	}
	args = append(args, 10, q.Page*10)
	rows, e := tx.QueryContext(ctx, "SELECT "+requestColumns+" FROM requests WHERE "+where+" ORDER BY at DESC,cli,request_key LIMIT ? OFFSET ?", args...)
	if e != nil {
		return out, ErrUnavailable
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		r, e := scanRequest(rows)
		if e != nil {
			return out, ErrUnavailable
		}
		out.Requests = append(out.Requests, r)
	}
	if rows.Err() != nil {
		return out, ErrUnavailable
	}

	return out, nil
}
