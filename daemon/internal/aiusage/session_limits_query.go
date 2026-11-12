package aiusage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// AnalyzeLimits reads a consistent, bounded limits projection without changing the ledger.
// Date-window aggregates include descendants; the trend ignores dates and excludes agents.
func (s *Store) AnalyzeLimits(ctx context.Context, from, to int64, keys []SessionKey, comparison ...LimitComparisonRange) (Analysis, error) {
	now := s.options.Now()
	out := Analysis{RetainedFrom: now.AddDate(0, 0, -93).UnixMilli()}
	if from <= 0 || to <= from || to-from > MaxQuerySpanMillis || len(keys) > 1 {
		return out, ErrRange
	}
	if len(comparison) > 1 {
		return out, ErrRange
	}
	if len(comparison) == 1 {
		prior := comparison[0]
		if prior.From <= 0 || prior.To <= prior.From || prior.To > from || prior.To-prior.From > MaxQuerySpanMillis {
			return out, ErrRange
		}
	}
	if len(keys) == 1 && !validSessionKey(keys[0]) {
		return out, ErrRange
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
	err = tx.QueryRowContext(ctx, `SELECT (SELECT value FROM meta WHERE key='revision'),(SELECT value FROM meta WHERE key='generation'),(SELECT value FROM meta WHERE key='catalog_revision'),(SELECT value FROM meta WHERE key='catalog_ready')`).Scan(&out.State.Revision, &out.State.Generation, &out.State.CatalogRevision, &out.State.CatalogReady)
	if err != nil {
		return out, ErrUnavailable
	}
	graph, err := readAgentGraph(ctx, tx)
	if err != nil {
		return out, ErrUnavailable
	}
	lines, err := readLimitBaselines(ctx, tx, out.RetainedFrom)
	if err != nil {
		return out, ErrUnavailable
	}
	// The upper bound is exclusive; include requests stamped in the current
	// millisecond, but never extend a completed caller-specified range.
	observed := min(to, now.UnixMilli()+1)
	facts, partial, err := readLimitFacts(ctx, tx, graph, lines, "r.at>=? AND r.at<?", []any{max(from, out.RetainedFrom), observed}, 100000)
	if err != nil {
		return out, ErrUnavailable
	}
	var selected *SessionKey
	if len(keys) == 1 {
		selected = &keys[0]
	}
	active, err := graph.active(ctx, tx, from, to)
	if err != nil {
		return out, ErrUnavailable
	}
	out.Limits = assembleLimits(facts, graph, active, selected)
	summary := &out.Limits.Summary
	summary.ObservedThrough = observed
	summary.Days = limitDays(facts, graph, selected, from, to, observed, out.RetainedFrom, partial, time.Local)
	summary.Comparison, err = readLimitComparison(ctx, tx, graph, lines, selected, from, to, observed, out.RetainedFrom, summary.Days, partial, time.Local, comparison...)
	if err != nil {
		return out, ErrUnavailable
	}
	out.Truncated = partial
	if selected != nil {
		main, truncated, e := readLimitFacts(ctx, tx, graph, lines, "r.cli=? AND r.session=? AND r.at>=? AND NOT EXISTS(SELECT 1 FROM session_agent_requests ar WHERE ar.cli=r.cli AND ar.request_key=r.request_key AND ar.session=r.session AND ar.agent<>'')", []any{selected.CLI, selected.Session, out.RetainedFrom}, 10000)
		if e != nil {
			return out, ErrUnavailable
		}
		out.Truncated = out.Truncated || truncated
		trend := limitTrend(main)
		out.Limits.Trend = &trend
		if len(main) > 0 {
			last := main[len(main)-1]
			current := &LimitCurrent{Context: last.row.Input, Band: limitBand(last.row.Input, last.lines)}
			if last.lines != nil {
				current.Limit = &last.lines.Limit
			}
			out.Limits.Summary.Current = current
		}
	}
	if err = tx.Commit(); err != nil {
		return out, ErrUnavailable
	}
	state := s.State()
	out.State.Backfilling = state.Backfilling
	out.State.Error = state.Error
	out.State.CatalogError = state.CatalogError
	out.Limits.AgentsIncomplete = out.Limits.AgentsIncomplete || state.CatalogError != ""
	return out, nil
}
func readLimitFacts(ctx context.Context, tx *sql.Tx, g agentGraph, lines map[string]*ModelLimit, where string, args []any, capRows int) ([]limitFact, bool, error) {
	query := `SELECT r.cli,r.session,r.request_key,r.model,coalesce(json_extract(r.details,'$.ReasoningEffort'),''),r.at,r.input,r.read,r.write,r.c0,r.c1,r.c2,coalesce(json_extract(r.details,'$.Billed[0]'),0),coalesce(json_extract(r.details,'$.Billed[1]'),0),coalesce(json_extract(r.details,'$.Billed[2]'),0),CASE WHEN json_extract(r.details,'$.Prices[0]') IS NOT NULL AND r.model<>'unknown' THEN 1 ELSE 0 END,coalesce(a.turn,''),coalesce(ar.agent,''),CASE WHEN json_extract(r.details,'$.ServiceSource') IN ('official_account','unknown') THEN 1 ELSE 0 END FROM requests r LEFT JOIN analysis_requests a ON a.cli=r.cli AND a.request_key=r.request_key LEFT JOIN session_agent_requests ar ON ar.cli=r.cli AND ar.request_key=r.request_key AND ar.session=r.session WHERE ` + where + fmt.Sprintf(" ORDER BY r.at,r.cli,r.request_key LIMIT %d", capRows+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	var facts []limitFact
	partial := false
	for rows.Next() {
		if len(facts) == capRows {
			partial = true
			break
		}
		var f limitFact
		r := &f.row
		if err = rows.Scan(&r.CLI, &r.Session, &r.RequestID, &r.Model, &r.Effort, &r.At, &r.Input, &r.Read, &r.Write, &r.Costs[0], &r.Costs[1], &r.Costs[2], &r.BilledCosts[0], &r.BilledCosts[1], &r.BilledCosts[2], &r.PricedRequests, &r.Turn, &f.agent, &r.UnconfirmedChannel); err != nil {
			return nil, false, err
		}
		r.Count = 1
		r.MaxInput = r.Input
		if r.Turn != "" {
			r.KnownTurnRequests = 1
		}
		source := SessionKey{r.CLI, r.Session}
		scopes := g.scopes(source)
		f.root = scopes[len(scopes)-1]
		r.SessionTitle = g.Catalog[source].Title
		if f.agent != "" {
			f.agentTitle = g.Names[agentKey{r.CLI, r.Session, f.agent}]
			if f.agentTitle == "" {
				f.agentTitle = f.agent
			}
			f.agent = r.CLI + ":" + r.Session + ":" + f.agent
		} else if f.root != source {
			f.agent = r.CLI + ":" + r.Session
			f.agentTitle = g.Catalog[source].Title
			if f.agentTitle == "" {
				f.agentTitle = r.Session
			}
		}
		if line, ok := lines[limitBaselineKey(r.Model, r.At)]; ok {
			f.lines = line
		} else {
			f.lines = modelLimit(r.Model)
		}
		facts = append(facts, f)
	}
	return facts, partial, rows.Err()
}
