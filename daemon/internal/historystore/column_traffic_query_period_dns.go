package historystore

import (
	"context"
	"fmt"
	"time"
)

func columnTrafficDNSPeriod(ctx context.Context, q ColumnQuery, f ColumnTrafficFilter) ([]ColumnTrafficPeriodGroup, error) {
	at := f.DetailAt
	if at.IsZero() {
		at = f.Until
	}
	clockArgs := []any{at.UnixNano(), f.From.UnixNano(), f.Until.UnixNano()}
	if _, err := q.ExecContext(ctx, "DROP TABLE IF EXISTS temp.traffic_dns_period"); err != nil {
		return nil, err
	}
	// Exactly the source facts used by TrafficMatches and latency coverage.
	// The covering index excludes process/search/raw/identity text entirely.
	query := `CREATE TEMP TABLE traffic_dns_period AS WITH clock AS (SELECT ? AS detail_ns,? AS from_ns,? AS until_ns),base AS (
SELECT flow_key,start_ns,closed_ns,observed_ns,latency_known,latency,establishment_state,establishment_ms,failure,
CASE WHEN (closed_ns IS NOT NULL AND closed_ns>detail_ns) OR(observed_ns IS NOT NULL AND observed_ns>detail_ns) THEN 1 ELSE 0 END AS future
FROM snap_col_traffic CROSS JOIN clock WHERE source_kind='dns' AND start_ns>=? AND start_ns<until_ns)
SELECT flow_key,start_ns,CASE WHEN future=1 THEN 0 ELSE latency_known END AS read_latency_known,latency AS read_latency,
CASE WHEN future=1 THEN '' ELSE establishment_state END AS read_establishment_state,establishment_ms,
CASE WHEN future=1 THEN '' ELSE failure END AS read_failure,CASE WHEN future=1 THEN NULL ELSE closed_ns END AS closed_ns
FROM base CROSS JOIN clock WHERE (closed_ns IS NULL OR future=1 OR closed_ns>from_ns) AND(observed_ns IS NULL OR future=1 OR observed_ns>from_ns)`
	if _, err := q.ExecContext(ctx, query, append(append([]any{}, clockArgs...), at.Add(-24*time.Hour).UnixNano())...); err != nil {
		return nil, err
	}
	if _, err := q.ExecContext(ctx, "CREATE INDEX temp.traffic_dns_period_start ON traffic_dns_period(start_ns ASC,flow_key ASC,read_latency)"); err != nil {
		return nil, err
	}
	cte := `WITH traffic_clock AS (SELECT ? AS detail_ns,? AS from_ns,? AS until_ns),traffic_selected AS (SELECT * FROM traffic_dns_period) `
	g := ColumnTrafficPeriodGroup{Object: "dns", Name: "域名解析", Kind: "dns", MatchesSearch: true}
	var active int64
	aggregate := cte + `SELECT count(*),sum(CASE WHEN start_ns>from_ns AND(read_failure<>'' OR read_establishment_state='failed')THEN 1 ELSE 0 END),sum(CASE WHEN start_ns>from_ns AND read_latency_known=1 THEN 1 ELSE 0 END),sum(CASE WHEN start_ns>from_ns AND read_latency_known=0 THEN 1 ELSE 0 END),sum(CASE WHEN start_ns<=from_ns THEN 1 ELSE 0 END),coalesce(max(CASE WHEN start_ns>from_ns AND read_latency_known=1 THEN read_latency ELSE 0 END),0),coalesce(max(CASE WHEN start_ns>from_ns AND read_establishment_state='ready' THEN establishment_ms ELSE 0 END),0),coalesce(max(start_ns>from_ns AND read_establishment_state='ready'),0),coalesce(max(max(CASE WHEN start_ns>from_ns THEN start_ns ELSE 0 END,CASE WHEN closed_ns>from_ns AND closed_ns<=until_ns THEN closed_ns ELSE 0 END)),0) FROM traffic_selected CROSS JOIN traffic_clock`
	var failed, valid, missing, earlier any
	if err := q.QueryRowContext(ctx, aggregate, clockArgs...).Scan(&g.Connections, &failed, &valid, &missing, &earlier, &g.Slowest, &g.Establishment, &g.EstablishmentKnown, &active); err != nil {
		return nil, err
	}
	if g.Connections == 0 {
		return nil, nil
	}
	for _, pair := range []struct {
		v    any
		dest *uint64
	}{{failed, &g.Failed}, {valid, &g.Valid}, {missing, &g.Missing}, {earlier, &g.Earlier}} {
		n, ok := pair.v.(int64)
		if !ok {
			return nil, fmt.Errorf("historystore: DNS aggregate type %T", pair.v)
		}
		*pair.dest = uint64(n)
	}
	if active != 0 {
		g.LastActive = time.Unix(0, active)
	}
	keys, err := columnTrafficKeys(ctx, q, "SELECT flow_key FROM traffic_dns_period ORDER BY start_ns DESC,flow_key ASC LIMIT 1", nil)
	if err != nil {
		return nil, err
	}
	facts, err := columnTrafficFetchKeys(ctx, q, f, keys)
	if err != nil {
		return nil, err
	}
	g.Representative = facts[0]
	g.Latency, err = columnTrafficPeriodLatency(ctx, q, cte, clockArgs, int(g.Valid))
	if err != nil {
		return nil, err
	}
	// Basis is complete, but independent of the row count/samples. Read only
	// these two saved strings for the selected keys, never full connection rows.
	basisWhere := `source_kind='dns' AND start_ns>=? AND start_ns<? AND (closed_ns IS NULL OR closed_ns>? OR observed_ns>?) AND(observed_ns IS NULL OR observed_ns>? OR closed_ns>?)`
	basisArgs := []any{at.Add(-24 * time.Hour).UnixNano(), f.Until.UnixNano(), f.From.UnixNano(), at.UnixNano(), f.From.UnixNano(), at.UnixNano()}
	queryBasis := `WITH b AS(SELECT rule,rule_source,count(*) AS connections FROM snap_col_traffic WHERE ` + basisWhere + ` GROUP BY rule,rule_source) SELECT b.rule,b.rule_source,(SELECT t.route_name FROM snap_col_traffic t WHERE t.rule=b.rule AND t.rule_source=b.rule_source AND ` + basisWhere + ` ORDER BY t.start_ns DESC,t.flow_key ASC LIMIT 1),b.connections FROM b ORDER BY b.rule||b.rule_source`
	rows, err := q.QueryContext(ctx, queryBasis, append(append([]any{}, basisArgs...), basisArgs...)...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var b ColumnTrafficBasis
		if err = rows.Scan(&b.Rule, &b.Source, &b.RouteName, &b.Connections); err != nil {
			_ = rows.Close()
			return nil, err
		}
		g.Bases = append(g.Bases, b)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	if f.Search != "" {
		var match bool
		err = q.QueryRowContext(ctx, `SELECT coalesce(max(pax_traffic_contains('域名解析 dns '||t.application||' '||t.process||' '||t.domain||' '||t.ip||' '||t.route_name||' '||t.rule||' '||t.rule_source,?)),0) FROM traffic_dns_period p JOIN snap_col_traffic t ON t.flow_key=p.flow_key`, f.Search).Scan(&match)
		if err != nil {
			return nil, err
		}
		g.MatchesSearch = match
	}
	return []ColumnTrafficPeriodGroup{g}, nil
}
