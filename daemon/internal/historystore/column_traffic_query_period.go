package historystore

import (
	"context"
	"time"

	"pax-connect/daemon/internal/config"
)

// ColumnTrafficPeriodGroup is the SQL aggregate used by hour/day summaries.
// Only one representative connection and the bounded legacy chart are decoded.
type ColumnTrafficPeriodGroup struct {
	Object, Name, Kind, Route, Direction                                 string
	Representative                                                       config.LogConnectionSummary
	Connections, Failed, Valid, Missing, Earlier, Slowest, Establishment uint64
	EstablishmentKnown                                                   bool
	MatchesSearch                                                        bool
	MixedExit                                                            bool
	LastActive                                                           time.Time
	Latency                                                              []ColumnTrafficLatency
	Bases                                                                []ColumnTrafficBasis
}
type ColumnTrafficLatency struct {
	At           time.Time
	Milliseconds uint64
}
type ColumnTrafficBasis struct {
	Rule, Source, RouteName string
	Connections             uint64
}

// TrafficPeriodGroups performs complete SQL grouping without retaining facts.
func (s *ColumnSnapshot) TrafficPeriodGroups(ctx context.Context, f ColumnTrafficFilter) ([]ColumnTrafficPeriodGroup, error) {
	var out []ColumnTrafficPeriodGroup
	f.AnyExit, f.Overlap = true, true
	err := s.WithRead(ctx, func(q ColumnQuery) error {
		f.stale = s.staleViews()
		if f.Dimension == "dns" {
			var err error
			out, err = columnTrafficDNSPeriod(ctx, q, f)
			return err
		}
		cte, args := columnTrafficScope(f, false)
		// The period summary reuses one narrow SQL working set for its row,
		// chart and rule aggregates. It does not repeatedly traverse immutable
		// version unions, and does not copy connection payload columns.
		{
			if _, err := q.ExecContext(ctx, "DROP TABLE IF EXISTS temp.traffic_period"); err != nil {
				return err
			}
			narrow := `flow_key,member_object,member_name,member_kind,read_route_id,read_direction,read_route_name,read_rule,read_rule_source,start_ns,read_failure,read_establishment_state,read_future,read_latency_known,read_latency,establishment_ms,closed_ns,application,process,domain,ip,member_search`
			if _, err := q.ExecContext(ctx, "CREATE TEMP TABLE traffic_period AS "+cte+"SELECT "+narrow+" FROM traffic_selected", args...); err != nil {
				return err
			}
			if _, err := q.ExecContext(ctx, "CREATE INDEX temp.traffic_period_group ON traffic_period(member_object,read_route_id,read_direction,start_ns DESC,flow_key ASC)"); err != nil {
				return err
			}
			if _, err := q.ExecContext(ctx, "CREATE INDEX temp.traffic_period_latency ON traffic_period(start_ns ASC,flow_key ASC,read_latency) WHERE read_latency_known=1"); err != nil {
				return err
			}
		}
		{
			cte = `WITH traffic_clock AS (SELECT ? AS detail_ns,? AS from_ns,? AS until_ns),traffic_selected AS (SELECT * FROM traffic_period) `
		}
		at := f.DetailAt
		if at.IsZero() {
			at = f.Until
		}
		{
			args = []any{at.UnixNano(), f.From.UnixNano(), f.Until.UnixNano()}
		}
		groupCols := "member_object,read_route_id,read_direction"
		if f.Dimension == "dns" || f.Dimension == "unknown" {
			groupCols = "member_object"
		}
		query := cte + `SELECT member_object,member_name,member_kind,read_route_id,read_direction,
count(*),sum(CASE WHEN start_ns>(SELECT from_ns FROM traffic_clock) AND (read_failure<>'' OR read_establishment_state='failed') THEN 1 ELSE 0 END),
sum(CASE WHEN start_ns>(SELECT from_ns FROM traffic_clock) AND read_latency_known=1 THEN 1 ELSE 0 END),
sum(CASE WHEN start_ns>(SELECT from_ns FROM traffic_clock) AND read_latency_known=0 THEN 1 ELSE 0 END),
sum(CASE WHEN start_ns<=(SELECT from_ns FROM traffic_clock) THEN 1 ELSE 0 END),
coalesce(max(CASE WHEN start_ns>(SELECT from_ns FROM traffic_clock) AND read_latency_known=1 THEN read_latency ELSE 0 END),0),
coalesce(max(CASE WHEN start_ns>(SELECT from_ns FROM traffic_clock) AND read_establishment_state='ready' THEN establishment_ms ELSE 0 END),0),
max(CASE WHEN start_ns>(SELECT from_ns FROM traffic_clock) AND read_establishment_state='ready' THEN 1 ELSE 0 END),
max(max(CASE WHEN start_ns>(SELECT from_ns FROM traffic_clock) AND start_ns<=(SELECT until_ns FROM traffic_clock) THEN start_ns ELSE 0 END,
CASE WHEN read_future=0 AND closed_ns IS NOT NULL AND closed_ns>(SELECT from_ns FROM traffic_clock) AND closed_ns<=(SELECT until_ns FROM traffic_clock) THEN closed_ns ELSE 0 END)),
min(flow_key),max(pax_traffic_contains(member_name||' '||member_object||' '||application||' '||process||' '||domain||' '||ip||' '||read_route_name||' '||read_rule||' '||read_rule_source,?)),count(DISTINCT read_route_id||char(0)||read_direction)>1 FROM traffic_selected GROUP BY ` + groupCols + ` ORDER BY member_object,read_route_id,read_direction`
		rows, err := q.QueryContext(ctx, query, append(append([]any{}, args...), f.Search)...)
		if err != nil {
			return err
		}
		type row struct {
			g   ColumnTrafficPeriodGroup
			key string
		}
		var groups []row
		for rows.Next() {
			var v row
			var active int64
			if err = rows.Scan(&v.g.Object, &v.g.Name, &v.g.Kind, &v.g.Route, &v.g.Direction, &v.g.Connections, &v.g.Failed, &v.g.Valid, &v.g.Missing, &v.g.Earlier, &v.g.Slowest, &v.g.Establishment, &v.g.EstablishmentKnown, &active, &v.key, &v.g.MatchesSearch, &v.g.MixedExit); err != nil {
				_ = rows.Close()
				return err
			}
			if active != 0 {
				v.g.LastActive = time.Unix(0, active)
			}
			groups = append(groups, v)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
		// Select all representative keys before decoding facts. One bounded
		// lookup per batch replaces a full joined fact query for every group.
		keys := make([]string, 0, len(groups))
		for i := range groups {
			v := &groups[i]
			gcte := `WITH traffic_clock AS (SELECT ? AS detail_ns,? AS from_ns,? AS until_ns),traffic_selected AS (SELECT * FROM traffic_period WHERE member_object=?`
			gargs := []any{at.UnixNano(), f.From.UnixNano(), f.Until.UnixNano(), v.g.Object}
			if f.Dimension != "dns" && f.Dimension != "unknown" {
				gcte += " AND read_route_id=? AND read_direction=?"
				gargs = append(gargs, v.g.Route, v.g.Direction)
			}
			gcte += ") "
			if err = q.QueryRowContext(ctx, gcte+"SELECT flow_key FROM traffic_selected ORDER BY start_ns DESC,flow_key ASC LIMIT 1", gargs...).Scan(&v.key); err != nil {
				return err
			}
			keys = append(keys, v.key)
		}
		factsByKey := make(map[string]config.LogConnectionSummary, len(keys))
		for start := 0; start < len(keys); start += 128 {
			facts, err := columnTrafficFetchKeys(ctx, q, f, keys[start:min(start+128, len(keys))])
			if err != nil {
				return err
			}
			for _, fact := range facts {
				factsByKey[fact.Fact.Key] = fact
			}
		}
		for _, v := range groups {
			v.g.Representative = factsByKey[v.key]
			gcte := `WITH traffic_clock AS (SELECT ? AS detail_ns,? AS from_ns,? AS until_ns),traffic_selected AS (SELECT * FROM traffic_period WHERE member_object=?`
			gargs := []any{at.UnixNano(), f.From.UnixNano(), f.Until.UnixNano(), v.g.Object}
			if f.Dimension != "dns" && f.Dimension != "unknown" {
				gcte += " AND read_route_id=? AND read_direction=?"
				gargs = append(gargs, v.g.Route, v.g.Direction)
			}
			gcte += ") "
			v.g.Latency, err = columnTrafficPeriodLatency(ctx, q, gcte, gargs, int(v.g.Valid))
			if err != nil {
				return err
			}
			br, err := q.QueryContext(ctx, gcte+`,basis_ranked AS (SELECT read_rule,read_rule_source,read_route_name,count(*) OVER(PARTITION BY read_rule,read_rule_source) AS connections,row_number() OVER(PARTITION BY read_rule,read_rule_source ORDER BY start_ns DESC,flow_key ASC) AS ordinal FROM traffic_selected) SELECT read_rule,read_rule_source,read_route_name,connections FROM basis_ranked WHERE ordinal=1 ORDER BY read_rule||read_rule_source`, gargs...)
			if err != nil {
				return err
			}
			for br.Next() {
				var b ColumnTrafficBasis
				if err = br.Scan(&b.Rule, &b.Source, &b.RouteName, &b.Connections); err != nil {
					_ = br.Close()
					return err
				}
				v.g.Bases = append(v.g.Bases, b)
			}
			err = br.Err()
			_ = br.Close()
			if err != nil {
				return err
			}
			out = append(out, v.g)
		}
		return nil
	})
	return out, err
}

func columnTrafficPeriodLatency(ctx context.Context, q ColumnQuery, cte string, args []any, total int) ([]ColumnTrafficLatency, error) {
	// Production reduction keeps endpoints, and the first min/max in each of
	// 127 consecutive equal-count buckets. Equal-time samples keep key ASC.
	reducer := config.NewTrafficLatencyReducer(total)
	rows, err := q.QueryContext(ctx, cte+"SELECT start_ns,read_latency FROM traffic_selected WHERE start_ns>(SELECT from_ns FROM traffic_clock) AND read_latency_known=1 ORDER BY start_ns ASC,flow_key ASC", args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var ns, ms int64
		if err = rows.Scan(&ns, &ms); err != nil {
			return nil, err
		}
		reducer.Observe(time.Unix(0, ns), uint64(ms))
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	var out []ColumnTrafficLatency
	for _, v := range reducer.Samples() {
		out = append(out, ColumnTrafficLatency{At: v.At, Milliseconds: v.Milliseconds})
	}
	return out, nil
}
