package aiusage

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
)

// SessionKey is a CLI-scoped native conversation identity.
type SessionKey struct{ CLI, Session string }

// AnalysisRow is a session/model/effort contribution, or one native turn contribution.
type AnalysisRow struct {
	CompletedTurnIDs                                              []string
	CLI, Session, SessionTitle, RequestID, Turn, Model, Effort    string
	At, Ended, MaxInput, Turns, KnownTurnRequests, PricedRequests int64
	Complete, Compressed                                          bool
	Summary
}

// Analysis contains bounded retained facts and their exact revision snapshot.
type Analysis struct {
	Rows         []AnalysisRow
	Requests     []AnalysisRow
	State        State
	Truncated    bool
	RetainedFrom int64
	ContextUsage []ContextUsage
	Limits       *SessionLimits
}

// Analyze aggregates in one read transaction. Time-window summaries are filtered by
// request time; selected trajectories ignore that window but respect retention.
func (s *Store) Analyze(ctx context.Context, from, to int64, keys []SessionKey) (Analysis, error) {
	out := Analysis{RetainedFrom: s.options.Now().AddDate(0, 0, -93).UnixMilli()}
	if from <= 0 || to <= from || to-from > MaxQuerySpanMillis || len(keys) > 5 {
		return out, ErrRange
	}
	for _, k := range keys {
		if k.Session == "" || len(k.Session) > 512 || (k.CLI != "claude" && k.CLI != "codex" && k.CLI != "dsh") {
			return out, ErrRange
		}
	}
	if s.RebuildRequired() {
		out.State = s.State()
		return out, nil
	}
	tx, err := s.r.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, ErrUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	if err = tx.QueryRowContext(ctx, "SELECT (SELECT value FROM meta WHERE key='revision'),(SELECT value FROM meta WHERE key='generation'),(SELECT value FROM meta WHERE key='catalog_revision'),(SELECT value FROM meta WHERE key='catalog_ready')").Scan(&out.State.Revision, &out.State.Generation, &out.State.CatalogRevision, &out.State.CatalogReady); err != nil {
		return out, ErrUnavailable
	}
	where := "r.at>=? AND r.at<?"
	args := []any{from, to}
	if len(keys) > 0 {
		var clauses []string
		args = nil
		for _, k := range keys {
			clauses = append(clauses, "(r.cli=? AND r.session=?)")
			args = append(args, k.CLI, k.Session)
		}
		where = "(" + strings.Join(clauses, " OR ") + ")"
	}
	// Group by the persisted JSON effort (not a parsed model name), and native turn only for trajectories.
	turn := "''"
	if len(keys) > 0 {
		turn = "coalesce(a.turn,'')"
	}
	query := `SELECT r.cli,r.session,` + turn + `,r.model,coalesce(json_extract(r.details,'$.ReasoningEffort'),''),min(r.at),max(r.at),max(r.input),count(DISTINCT CASE WHEN t.ended>0 THEN a.turn END),sum(CASE WHEN a.turn IS NOT NULL THEN 1 ELSE 0 END),min(CASE WHEN t.ended>0 THEN 1 ELSE 0 END),max(coalesce(t.compressed,0)),sum(CASE WHEN json_extract(r.details,'$.Prices[0]') IS NOT NULL AND r.model<>'unknown' THEN 1 ELSE 0 END),sum(r.succeeded),sum(r.input),sum(r.read),sum(r.write),sum(r.output),sum(r.c0),sum(r.c1),sum(r.c2),sum(r.c3),sum(r.cost),sum(CASE WHEN r.succeeded AND r.elapsed IS NOT NULL THEN r.elapsed ELSE 0 END),sum(CASE WHEN r.succeeded AND r.elapsed IS NOT NULL THEN 1 ELSE 0 END),count(*),sum(coalesce(json_extract(r.details,'$.Billed[0]'),0)),sum(coalesce(json_extract(r.details,'$.Billed[1]'),0)),sum(coalesce(json_extract(r.details,'$.Billed[2]'),0)),sum(coalesce(json_extract(r.details,'$.Billed[3]'),0)),json_group_array(DISTINCT CASE WHEN t.ended>0 THEN a.turn END) FROM requests r LEFT JOIN analysis_requests a ON a.cli=r.cli AND a.request_key=r.request_key LEFT JOIN analysis_turns t ON t.cli=a.cli AND t.session=a.session AND t.turn=a.turn WHERE ` + where + ` GROUP BY r.cli,r.session,` + turn + `,r.model,coalesce(json_extract(r.details,'$.ReasoningEffort'),''),CASE WHEN t.ended>0 THEN 1 ELSE 0 END ORDER BY min(r.at) DESC,r.cli,r.session,r.model LIMIT 10001`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return out, ErrUnavailable
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var v AnalysisRow
		var completed string
		if err = rows.Scan(&v.CLI, &v.Session, &v.Turn, &v.Model, &v.Effort, &v.At, &v.Ended, &v.MaxInput, &v.Turns, &v.KnownTurnRequests, &v.Complete, &v.Compressed, &v.PricedRequests, &v.Success, &v.Input, &v.Read, &v.Write, &v.Output, &v.Costs[0], &v.Costs[1], &v.Costs[2], &v.Costs[3], &v.Cost, &v.ElapsedSum, &v.ElapsedCount, &v.Count, &v.BilledCosts[0], &v.BilledCosts[1], &v.BilledCosts[2], &v.BilledCosts[3], &completed); err != nil {
			return out, ErrUnavailable
		}
		var ids []*string
		if err = json.Unmarshal([]byte(completed), &ids); err != nil {
			return out, ErrUnavailable
		}
		for _, id := range ids {
			if id != nil {
				v.CompletedTurnIDs = append(v.CompletedTurnIDs, *id)
			}
		}
		out.Rows = append(out.Rows, v)
	}
	if err = rows.Err(); err != nil {
		return out, ErrUnavailable
	}
	_ = rows.Close()
	if len(keys) > 0 {
		requestQuery := strings.Replace(query, " FROM requests r", ",r.request_key FROM requests r", 1)
		requestQuery = requestQuery[:strings.Index(requestQuery, " GROUP BY ")] + " GROUP BY r.cli,r.request_key ORDER BY r.at,r.cli,r.request_key LIMIT 10001"
		rows, err = tx.QueryContext(ctx, requestQuery, args...)
		if err != nil {
			return out, ErrUnavailable
		}
		for rows.Next() {
			var v AnalysisRow
			var completed string
			if err = rows.Scan(&v.CLI, &v.Session, &v.Turn, &v.Model, &v.Effort, &v.At, &v.Ended, &v.MaxInput, &v.Turns, &v.KnownTurnRequests, &v.Complete, &v.Compressed, &v.PricedRequests, &v.Success, &v.Input, &v.Read, &v.Write, &v.Output, &v.Costs[0], &v.Costs[1], &v.Costs[2], &v.Costs[3], &v.Cost, &v.ElapsedSum, &v.ElapsedCount, &v.Count, &v.BilledCosts[0], &v.BilledCosts[1], &v.BilledCosts[2], &v.BilledCosts[3], &completed, &v.RequestID); err != nil {
				return out, ErrUnavailable
			}
			var ids []*string
			if err = json.Unmarshal([]byte(completed), &ids); err != nil {
				return out, ErrUnavailable
			}
			for _, id := range ids {
				if id != nil {
					v.CompletedTurnIDs = append(v.CompletedTurnIDs, *id)
				}
			}
			out.Requests = append(out.Requests, v)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return out, ErrUnavailable
		}
		if len(out.Requests) > 10000 {
			out.Requests = out.Requests[:10000]
			out.Truncated = true
		}
	}

	if len(keys) > 0 {
		var clauses []string
		var eventArgs []any
		for _, k := range keys {
			clauses = append(clauses, "(t.cli=? AND t.session=?)")
			eventArgs = append(eventArgs, k.CLI, k.Session)
		}
		events, e := tx.QueryContext(ctx, "SELECT t.cli,t.session,t.turn,t.at,t.ended,t.compressed FROM analysis_turns t WHERE ("+strings.Join(clauses, " OR ")+") AND NOT EXISTS(SELECT 1 FROM analysis_requests a JOIN requests r ON r.cli=a.cli AND r.request_key=a.request_key WHERE a.cli=t.cli AND a.session=t.session AND a.turn=t.turn) ORDER BY t.at LIMIT 10001", eventArgs...)
		if e != nil {
			return out, ErrUnavailable
		}
		for events.Next() {
			var v AnalysisRow
			if e = events.Scan(&v.CLI, &v.Session, &v.Turn, &v.At, &v.Ended, &v.Compressed); e != nil {
				_ = events.Close()
				return out, ErrUnavailable
			}
			v.Complete = v.Ended > 0
			if v.Complete {
				v.CompletedTurnIDs = []string{v.Turn}
			}
			out.Rows = append(out.Rows, v)
		}
		e = events.Err()
		_ = events.Close()
		if e != nil {
			return out, ErrUnavailable
		}
	}
	out.Truncated = out.Truncated || len(out.Rows) > 10000
	if len(out.Rows) > 10000 {
		out.Rows = out.Rows[:10000]
	}
	if len(keys) == 0 {
		var partial bool
		out.ContextUsage, partial, err = contextUsage(ctx, tx, from, to)
		if err != nil {
			return out, ErrUnavailable
		}
		out.Truncated = out.Truncated || partial
	}
	sort.SliceStable(out.Rows, func(i, j int) bool { return out.Rows[i].At < out.Rows[j].At })
	if err = tx.Commit(); err != nil {
		return out, ErrUnavailable
	}
	s.fillSessionTitles(ctx, &out)
	state := s.State()
	out.State.Backfilling = state.Backfilling
	out.State.Error = state.Error
	out.State.CatalogError = state.CatalogError
	if state.CatalogError != "" {
		for i := range out.ContextUsage {
			out.ContextUsage[i].SubagentsIncomplete = true
		}
	}
	return out, nil
}
