package historystore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"pax-connect/daemon/internal/config"
)

// columnTrafficScope uses only write-derived scalar query material. The full
// immutable facts are fetched after SQL has fixed the page/sample keys.
func columnTrafficScope(f ColumnTrafficFilter, recent bool) (string, []any) {
	at := f.DetailAt
	if at.IsZero() {
		at = f.Until
	}
	args := []any{at.UnixNano(), f.From.UnixNano(), f.Until.UnixNano()}
	cte := `WITH traffic_clock AS (SELECT ? AS detail_ns,? AS from_ns,? AS until_ns),`
	joined := f.Dimension != "" || f.Object != ""
	cols := `v.*`
	from := `snap_col_traffic v CROSS JOIN traffic_clock t`
	if f.Dimension == "dns" {
		joined = false
		cols += `,v.route_id AS member_route,v.direction AS member_direction,'dns' AS member_dimension,'dns' AS member_object,'域名解析' AS member_name,'dns' AS member_kind,'' AS member_search`
	} else if joined {
		from = `snap_col_objects o JOIN snap_col_members m ON m.object_id=o.id JOIN snap_col_traffic v ON v.flow_key=m.flow_key CROSS JOIN traffic_clock t`
		if f.stale && f.Object != "" && (f.Dimension != "ip" || f.Object != "unknown") {
			// A single object's members are tiny next to the whole tables. When a
			// snapshot is older than the writer, snap_* are UNION views that SQLite
			// cannot probe by join key: it scans/materializes every row per query.
			// Constant IN selectors are pushed into each UNION term, so resolve the
			// object's members and facts first and join only those small sets.
			dim := f.Dimension
			if dim == "" {
				dim = "domain"
			}
			cte += `traffic_object_members AS MATERIALIZED (SELECT * FROM snap_col_members WHERE object_id IN (SELECT id FROM snap_col_objects WHERE dimension=? AND object_key=?)),`
			from = `snap_col_objects o JOIN traffic_object_members m ON m.object_id=o.id JOIN (SELECT * FROM snap_col_traffic WHERE flow_key IN (SELECT flow_key FROM traffic_object_members)) v ON v.flow_key=m.flow_key CROSS JOIN traffic_clock t`
			args = append(args, dim, f.Object)
		}
		cols += `,m.route AS member_route,m.direction AS member_direction,o.dimension AS member_dimension,o.object_key AS member_object,o.name AS member_name,o.kind AS member_kind,m.search AS member_search`
	} else {
		cols += `,'' AS member_route,'' AS member_direction,'' AS member_dimension,'' AS member_object,'' AS member_name,'' AS member_kind,'' AS member_search`
	}
	corrected := `v.correction_ns<>0 AND v.correction_ns<=t.detail_ns`
	for _, v := range []struct{ raw, correct string }{{"direction", "corrected_direction"}, {"route_id", "corrected_route"}, {"route_name", "corrected_name"}, {"rule", "corrected_rule"}, {"rule_source", "corrected_source"}} {
		cols += ",CASE WHEN " + corrected + " THEN v." + v.correct + " ELSE v." + v.raw + " END AS read_" + v.raw
	}
	cols += `,CASE WHEN (v.observed_ns IS NOT NULL AND v.observed_ns>t.detail_ns) OR (v.closed_ns IS NOT NULL AND v.closed_ns>t.detail_ns) THEN 1 ELSE 0 END AS read_future`
	where := []string{"1"}
	if f.Dimension == "dns" {
		where = append(where, "v.source_kind='dns'")
	}
	if f.ConnectionKey != "" {
		where = append(where, "v.flow_key=?")
		args = append(args, f.ConnectionKey)
	} else {
		where = append(where, "v.start_ns>=?", "v.start_ns<=t.detail_ns")
		args = append(args, at.Add(-24*time.Hour).UnixNano())
	}
	if joined {
		dim := f.Dimension
		if dim == "" {
			dim = "domain"
		}
		if dim == "ip" && f.Object == "unknown" {
			where = append(where, "o.dimension='domain' AND v.ip=''")
		} else {
			where = append(where, "o.dimension=?")
			args = append(args, dim)
			if f.Object != "" {
				where = append(where, "o.object_key=?")
				args = append(args, f.Object)
			}
		}
	}
	cte += `traffic_base AS (SELECT ` + cols + ` FROM ` + from + ` WHERE ` + strings.Join(where, " AND ") + `),traffic_resolved AS (SELECT b.*,CASE WHEN read_future=1 THEN 0 ELSE latency_known END AS read_latency_known,latency AS read_latency,CASE WHEN read_future=1 THEN '' ELSE failure END AS read_failure,CASE WHEN read_future=1 THEN '' ELSE establishment_state END AS read_establishment_state,CASE WHEN read_future=1 THEN future_dns_group ELSE dns_group END AS dns_group_id FROM traffic_base b),`
	where = []string{"1"}
	dns := f.Dimension == "dns"
	if joined {
		where = append(where, "member_route=read_route_id", "member_direction=read_direction")
		if !f.AnyExit && !dns && f.Dimension != "unknown" {
			where = append(where, "read_route_id=?", "read_direction=?")
			args = append(args, f.Route, f.MemberDirection)
		}
	}
	if !dns && f.Direction != "" && f.Direction != "all" {
		where = append(where, "read_direction=?")
		args = append(args, f.Direction)
	}
	if dns && !f.Overlap || recent {
		where = append(where, "start_ns>(SELECT from_ns FROM traffic_clock)", "start_ns<=(SELECT until_ns FROM traffic_clock)")
	}
	if recent {
		where = append(where, "read_latency_known=1")
		if f.Buckets != nil {
			if len(f.Buckets) == 0 {
				where = append(where, "0")
			} else {
				where = append(where, "(start_ns/300000000000) IN ("+strings.TrimSuffix(strings.Repeat("?,", len(f.Buckets)), ",")+")")
				for _, b := range f.Buckets {
					args = append(args, b/300000)
				}
			}
		}
	}
	if f.Overlap {
		where = append(where, "start_ns<(SELECT until_ns FROM traffic_clock)", "(observed_ns IS NULL OR read_future=1 OR observed_ns>(SELECT from_ns FROM traffic_clock))", "(closed_ns IS NULL OR read_future=1 OR closed_ns>(SELECT from_ns FROM traffic_clock))")
	}
	if f.DNSGroupID != "" {
		where = append(where, "dns_group_id=?")
		args = append(args, f.DNSGroupID)
	}
	return cte + `traffic_selected AS (SELECT * FROM traffic_resolved WHERE ` + strings.Join(where, " AND ") + `) `, args
}

func columnTrafficKeys(ctx context.Context, q ColumnQuery, stmt string, args []any) ([]string, error) {
	rows, err := q.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			return nil, err
		}
		out = append(out, key)
	}
	return out, rows.Err()
}

func columnTrafficFetchKeys(ctx context.Context, q ColumnQuery, f ColumnTrafficFilter, keys []string) ([]config.LogConnectionSummary, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	f.Dimension, f.Object, f.DNSGroupID, f.ConnectionKey = "", "", "", ""
	f.Direction = "" // the completed SQL selector already fixed each output key
	f.keys = keys
	cte, args := columnTrafficCTE(f, false)
	rows, err := columnTrafficReadRows(ctx, q, cte+"SELECT "+columnTrafficSelect("s")+" FROM traffic_selected s", args)
	if err != nil {
		return nil, err
	}
	byKey := map[string]config.LogConnectionSummary{}
	for _, v := range rows {
		byKey[v.Fact.Key] = v
	}
	out := make([]config.LogConnectionSummary, 0, len(keys))
	for _, key := range keys {
		v, ok := byKey[key]
		if !ok {
			return nil, fmt.Errorf("historystore: missing selected traffic fact")
		}
		out = append(out, v)
	}
	return out, nil
}
