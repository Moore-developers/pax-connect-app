package aiusage

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
)

// ContextBand attributes a whole request by its input size, excluding output costs.
type ContextBand struct {
	Input, OfficialCost, ChannelCost, Requests, PricedRequests, UnconfirmedChannel int64
}

// ContextUsage is one model or total, scoped to a session or the entire date window.
type ContextUsage struct {
	CLI, Session, Model, Effort        string
	Bands                              [3]ContextBand
	Requests, Turns, KnownTurnRequests int64
	Subagents                          int64
	SubagentBands                      [3]ContextBand
	SubagentsIncomplete                bool
}

type contextKey struct{ cli, session, model, effort string }
type contextTurn struct{ cli, session, turn string }
type contextAggregate struct {
	value ContextUsage
	turns map[contextTurn]struct{}
}

func contextUsage(ctx context.Context, tx *sql.Tx, from, to int64) ([]ContextUsage, bool, error) {
	graph, err := readAgentGraph(ctx, tx)
	if err != nil {
		return nil, false, err
	}
	active, err := graph.active(ctx, tx, from, to)
	if err != nil {
		return nil, false, err
	}
	// Aggregate before returning any data to the App. Turn sets are deduplicated again
	// across bands/models for the total; output and today's Profile never enter costs.
	rows, err := tx.QueryContext(ctx, `SELECT r.cli,r.session,r.model,coalesce(json_extract(r.details,'$.ReasoningEffort'),''),coalesce(ar.agent,''),
 CASE WHEN r.input<=150000 THEN 0 WHEN r.input<=250000 THEN 1 ELSE 2 END AS band,
 sum(r.input),sum(r.c0+r.c1+r.c2),
 sum(coalesce(json_extract(r.details,'$.Billed[0]'),0)+coalesce(json_extract(r.details,'$.Billed[1]'),0)+coalesce(json_extract(r.details,'$.Billed[2]'),0)),
 count(*),sum(CASE WHEN json_extract(r.details,'$.Prices[0]') IS NOT NULL AND r.model<>'unknown' THEN 1 ELSE 0 END),
 sum(CASE WHEN a.turn IS NOT NULL THEN 1 ELSE 0 END),json_group_array(DISTINCT a.turn),sum(CASE WHEN json_extract(r.details,'$.ServiceSource') IN ('official_account','unknown') THEN 1 ELSE 0 END)
 FROM requests r LEFT JOIN analysis_requests a ON a.cli=r.cli AND a.request_key=r.request_key LEFT JOIN session_agent_requests ar ON ar.cli=r.cli AND ar.request_key=r.request_key AND ar.session=r.session
 WHERE r.at>=? AND r.at<? GROUP BY r.cli,r.session,r.model,coalesce(json_extract(r.details,'$.ReasoningEffort'),''),coalesce(ar.agent,''),band
 ORDER BY r.cli,r.session,r.model,4,5,band LIMIT 10001`, from, to)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	groups := map[contextKey]*contextAggregate{}
	ensure := func(key contextKey) *contextAggregate {
		group := groups[key]
		if group == nil {
			group = &contextAggregate{value: ContextUsage{CLI: key.cli, Session: key.session, Model: key.model, Effort: key.effort}, turns: map[contextTurn]struct{}{}}
			groups[key] = group
		}
		return group
	}
	count := 0
	partial := false
	for rows.Next() {
		count++
		if count > 10000 {
			partial = true
			break
		}
		var cli, session, model, effort, rawTurns, agentID string
		var band int
		var known int64
		var v ContextBand
		if err = rows.Scan(&cli, &session, &model, &effort, &agentID, &band, &v.Input, &v.OfficialCost, &v.ChannelCost, &v.Requests, &v.PricedRequests, &known, &rawTurns, &v.UnconfirmedChannel); err != nil {
			return nil, false, err
		}
		var turns []*string
		if err = json.Unmarshal([]byte(rawTurns), &turns); err != nil {
			return nil, false, err
		}
		source := SessionKey{cli, session}
		agent, isAgent := graph.Agents[agentKey{cli, session, agentID}]
		if !isAgent {
			agent, isAgent = graph.Sessions[source]
		}
		if isAgent {
			active[agent.Key] = true
		}
		keys := []contextKey{{}, {model: model, effort: effort}}
		for _, scope := range graph.scopes(source) {
			keys = append(keys, contextKey{cli: scope.CLI, session: scope.Session}, contextKey{cli: scope.CLI, session: scope.Session, model: model, effort: effort})
		}
		for _, key := range keys {
			group := ensure(key)
			if isAgent && (key.session == "" || cli == "claude" || key.session != source.Session) {
				b := &group.value.SubagentBands[band]
				b.Input += v.Input
				b.OfficialCost += v.OfficialCost
				b.ChannelCost += v.ChannelCost
				b.Requests += v.Requests
				b.PricedRequests += v.PricedRequests
				b.UnconfirmedChannel += v.UnconfirmedChannel
			}
			b := &group.value.Bands[band]
			b.Input += v.Input
			b.OfficialCost += v.OfficialCost
			b.ChannelCost += v.ChannelCost
			b.Requests += v.Requests
			b.PricedRequests += v.PricedRequests
			b.UnconfirmedChannel += v.UnconfirmedChannel
			group.value.Requests += v.Requests
			group.value.KnownTurnRequests += known
			for _, turn := range turns {
				if turn != nil {
					group.turns[contextTurn{cli, session, *turn}] = struct{}{}
				}
			}
		}
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	for key := range active {
		a, ok := graph.Agents[key]
		if !ok {
			continue
		}
		all := ensure(contextKey{})
		all.value.Subagents++
		all.value.SubagentsIncomplete = all.value.SubagentsIncomplete || graph.incomplete(a)
		for _, scope := range graph.owners(a) {
			v := ensure(contextKey{cli: scope.CLI, session: scope.Session})
			v.value.Subagents++
			v.value.SubagentsIncomplete = v.value.SubagentsIncomplete || graph.incomplete(a)
		}
	}
	for key, group := range groups {
		if key.model != "" {
			continue
		}
		status := graph.Catalog[SessionKey{key.cli, key.session}].Status
		group.value.SubagentsIncomplete = group.value.SubagentsIncomplete || graph.Incomplete || status == "source_missing" || status == "source_unavailable"
	}
	out := make([]ContextUsage, 0, len(groups))
	for _, group := range groups {
		group.value.Turns = int64(len(group.turns))
		out = append(out, group.value)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.CLI != b.CLI {
			return a.CLI < b.CLI
		}
		if a.Session != b.Session {
			return a.Session < b.Session
		}
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		return a.Effort < b.Effort
	})
	return out, partial, nil
}
