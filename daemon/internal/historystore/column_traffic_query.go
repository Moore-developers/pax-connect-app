package historystore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"pax-connect/daemon/internal/config"
)

// ColumnTrafficFilter selects retained evidence for one frozen displayed object.
// Direction is the user's filter; MemberDirection/Route identify the summary row.
// Buckets nil means unrestricted; an empty non-nil slice means no matching bucket.
type ColumnTrafficFilter struct {
	Dimension, Object, Route, MemberDirection, Direction string
	ConnectionKey, DNSGroupID                            string
	Search                                               string
	From, Until, DetailAt                                time.Time
	Buckets                                              []int64  // five-minute bucket starts, Unix milliseconds
	Overlap                                              bool     // period summaries apply TrafficMatches; retained details do not
	AnyExit                                              bool     // select every exit while discovering period summary rows
	keys                                                 []string // already selected immutable page keys
	stale                                                bool     // snapshot older than writer: snap_* are UNION views
}

// ColumnTrafficPage contains only the selected page and its same-version count.
type ColumnTrafficPage struct {
	Total int
	Items []config.LogConnectionSummary
}

// ColumnTrafficDNSGroup keeps one group's count and latest actual observation.
// The controller applies the existing DNS presentation to Latest.
type ColumnTrafficDNSGroup struct {
	ID      string
	Queries uint64
	Latest  config.LogConnectionSummary
}

func columnTrafficCTE(f ColumnTrafficFilter, recent bool) (string, []any) {
	at := f.DetailAt
	if at.IsZero() {
		at = f.Until
	}
	args := []any{at.UnixNano(), f.From.UnixNano(), f.Until.UnixNano()}
	clock := `WITH traffic_clock AS (SELECT ? AS detail_ns,? AS from_ns,? AS until_ns),`
	joined := f.Dimension != "" || f.Object != ""
	cols := "c.*," + strings.TrimPrefix(prefixedColumns("i", identityColumnNames), "i.flow_key,")
	from := `snap_col_connections c JOIN snap_col_identities i ON i.id=c.identity_id`
	start := "c.started_ns"
	if joined {
		from = `snap_col_members m JOIN snap_col_objects o ON o.id=m.object_id JOIN snap_col_connections c ON c.flow_key=m.flow_key JOIN snap_col_identities i ON i.id=c.identity_id`
		cols += `,m.route AS member_route,m.direction AS member_direction,o.dimension AS member_dimension,o.object_key AS member_object,o.name AS member_name,o.kind AS member_kind,m.search AS member_search`
		start = "m.started_ns"
	} else {
		cols += `,'' AS member_route,'' AS member_direction,'' AS member_dimension,'' AS member_object,'' AS member_name,'' AS member_kind,'' AS member_search`
	}
	cols += "," + start + " AS start_ns"
	events, identities := `snap_col_events`, `snap_col_identities`
	if f.stale && len(f.keys) > 0 && !joined {
		// A snapshot older than the writer exposes these tables as UNION views.
		// SQLite cannot probe a UNION view by join key, so it materializes every
		// retained connection, identity and event (and indexes them) per query.
		// Constant IN selectors are pushed into each UNION term and use the
		// primary keys: resolve only the selected keys' rows once, then join them.
		marks := strings.TrimSuffix(strings.Repeat("?,", len(f.keys)), ",")
		clock += `traffic_pick_connections AS MATERIALIZED (SELECT * FROM snap_col_connections WHERE flow_key IN (` + marks + `)),` +
			`traffic_pick_events AS MATERIALIZED (SELECT * FROM snap_col_events WHERE event_id IN (SELECT last_event_id FROM traffic_pick_connections)),` +
			`traffic_pick_identities AS MATERIALIZED (SELECT * FROM snap_col_identities WHERE id IN (SELECT identity_id FROM traffic_pick_connections UNION SELECT identity_id FROM traffic_pick_events)),`
		for _, key := range f.keys {
			args = append(args, key)
		}
		from = `traffic_pick_connections c JOIN traffic_pick_identities i ON i.id=c.identity_id`
		events, identities = `traffic_pick_events`, `traffic_pick_identities`
	}
	from += ` LEFT JOIN ` + events + ` le ON i.direction='unknown' AND i.route_id='' AND c.failure='rule_rejected' AND le.event_id=c.last_event_id LEFT JOIN ` + identities + ` li ON li.id=le.identity_id CROSS JOIN traffic_clock t`
	rejected := `i.direction='unknown' AND i.route_id='' AND c.failure='rule_rejected' AND le.flow_present=1 AND li.flow_key=c.flow_key AND li.direction='blocked' AND le.failure='rule_rejected' AND le.at<>'0001-01-01T00:00:00Z' AND le.at_ns<=t.detail_ns`
	for _, col := range []string{"direction", "route_id", "route_name", "rule", "rule_source"} {
		cols += ",CASE WHEN " + rejected + " THEN li." + col + " ELSE i." + col + " END AS read_" + col
	}
	cols += `,CASE WHEN (c.observed_until IS NOT NULL AND pax_time_ns(c.observed_until)>t.detail_ns) OR (c.closed_at IS NOT NULL AND pax_time_ns(c.closed_at)>t.detail_ns) THEN 1 ELSE 0 END AS read_future`
	baseWhere := []string{"1"}
	if f.ConnectionKey != "" {
		baseWhere = append(baseWhere, "c.flow_key=?")
		args = append(args, f.ConnectionKey)
	}
	if len(f.keys) > 0 {
		baseWhere = append(baseWhere, "c.flow_key IN ("+strings.TrimSuffix(strings.Repeat("?,", len(f.keys)), ",")+")")
		for _, key := range f.keys {
			args = append(args, key)
		}
	}
	if f.ConnectionKey == "" && len(f.keys) == 0 || joined {
		baseWhere = append(baseWhere, start+">=?", start+"<=t.detail_ns")
		args = append(args, at.Add(-24*time.Hour).UnixNano())
	}
	if joined {
		dimension := f.Dimension
		if dimension == "" {
			dimension = "domain"
		}
		// Period IP compatibility includes connections whose IP was not recorded.
		if dimension == "ip" && f.Object == "unknown" {
			baseWhere = append(baseWhere, "o.dimension='domain' AND i.ip=''")
		} else {
			baseWhere = append(baseWhere, "o.dimension=?")
			args = append(args, dimension)
			if f.Object != "" {
				baseWhere = append(baseWhere, "o.object_key=?")
				args = append(args, f.Object)
			}
		}
	}
	base := `traffic_base AS (SELECT ` + cols + ` FROM ` + from + ` WHERE ` + strings.Join(baseWhere, " AND ") + `),`
	resolved := `traffic_resolved AS (SELECT b.*,CASE WHEN read_future=1 THEN 0 WHEN response_known=1 THEN 1 WHEN establishment_state='response_ready' AND evidence='first_response' THEN 1 ELSE 0 END AS read_latency_known,CASE WHEN response_known=1 THEN response_ms ELSE establishment_ms END AS read_latency,CASE WHEN read_future=1 THEN '' ELSE failure END AS read_failure,CASE WHEN read_future=1 THEN '' ELSE establishment_state END AS read_establishment_state FROM traffic_base b),`
	where := []string{"1"}
	dns := f.Dimension == "dns"
	if joined {
		where = append(where, "member_route=read_route_id", "member_direction=read_direction")
		if !f.AnyExit && f.Dimension != "dns" && f.Dimension != "unknown" {
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
		where = append(where, "start_ns<(SELECT until_ns FROM traffic_clock)", "(observed_until IS NULL OR read_future=1 OR pax_time_ns(observed_until)>(SELECT from_ns FROM traffic_clock))", "(closed_at IS NULL OR read_future=1 OR pax_time_ns(closed_at)>(SELECT from_ns FROM traffic_clock))")
	}
	if f.DNSGroupID != "" {
		where = append(where, "pax_dns_group(domain,dns_type,CASE WHEN dns_result<>'' THEN dns_result WHEN read_failure<>'' THEN read_failure ELSE '结果未记录' END)=?")
		args = append(args, f.DNSGroupID)
	}
	// A membership may contain original and corrected attribution, but requiring
	// member route/direction to equal the resolved fact selects exactly one of
	// those versions; production's member writer deduplicates the object key.
	return clock + base + resolved + `traffic_selected AS (SELECT * FROM traffic_resolved WHERE ` + strings.Join(where, " AND ") + `) `, args
}

func columnTrafficSelect(alias string) string {
	names := strings.Split(connectionColumnNames+","+stateColumnNames+","+metricsColumnNames+","+identityColumnNames, ",")
	for i, n := range names {
		switch n {
		case "direction", "route_id", "route_name", "rule", "rule_source":
			names[i] = alias + ".read_" + n
		case "closed_at", "observed_until":
			names[i] = "CASE WHEN " + alias + ".read_future=1 THEN NULL ELSE " + alias + "." + n + " END"
		case "response_known":
			names[i] = "CASE WHEN " + alias + ".read_future=1 THEN 0 ELSE " + alias + "." + n + " END"
		case "state_unknown":
			names[i] = "CASE WHEN " + alias + ".read_future=1 THEN 1 ELSE " + alias + "." + n + " END"
		case "establishment_state", "failure", "phase":
			names[i] = "CASE WHEN " + alias + ".read_future=1 THEN '' ELSE " + alias + "." + n + " END"
		default:
			names[i] = alias + "." + n
		}
	}
	return strings.Join(names, ",")
}

func columnTrafficReadRows(ctx context.Context, q ColumnQuery, sql string, args []any) ([]config.LogConnectionSummary, error) {
	rows, err := q.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	var out []config.LogConnectionSummary
	for rows.Next() {
		v, e := scanColumnConnection(rows)
		if e != nil {
			_ = rows.Close()
			return nil, e
		}
		out = append(out, v)
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	for i := range out {
		f := &out[i].Fact
		if f.DNS == nil || f.DNS.Answers == nil {
			continue
		}
		r, e := q.QueryContext(ctx, "SELECT value FROM snap_col_answers WHERE owner_kind=1 AND owner_key=? ORDER BY ordinal", f.Key)
		if e != nil {
			return nil, e
		}
		for r.Next() {
			var answer string
			if e = r.Scan(&answer); e != nil {
				_ = r.Close()
				return nil, e
			}
			f.DNS.Answers = append(f.DNS.Answers, answer)
		}
		e = r.Err()
		_ = r.Close()
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}

// TrafficConnections selects its count and page from one frozen SQL relation.
func (s *ColumnSnapshot) TrafficConnections(ctx context.Context, f ColumnTrafficFilter, offset, limit int) (ColumnTrafficPage, error) {
	var page ColumnTrafficPage
	if offset < 0 || limit < 1 || limit > 200 {
		return page, fmt.Errorf("historystore: invalid traffic page")
	}
	err := s.WithRead(ctx, func(q ColumnQuery) error {
		f.stale = s.staleViews()
		cte, args := columnTrafficScope(f, false)
		if err := q.QueryRowContext(ctx, cte+"SELECT count(*) FROM traffic_selected", args...).Scan(&page.Total); err != nil {
			return err
		}
		var err error
		keys, e := columnTrafficKeys(ctx, q, cte+"SELECT flow_key FROM traffic_selected ORDER BY start_ns DESC,flow_key ASC LIMIT ? OFFSET ?", append(append([]any{}, args...), limit, offset))
		if e != nil {
			return e
		}
		page.Items, err = columnTrafficFetchKeys(ctx, q, f, keys)
		return err
	})
	return page, err
}

// TrafficConnectionCount reuses the exact selector without decoding an unused
// probe row. Page-bound validation remains in the controller's cursor contract.
func (s *ColumnSnapshot) TrafficConnectionCount(ctx context.Context, f ColumnTrafficFilter) (int, error) {
	var total int
	err := s.WithRead(ctx, func(q ColumnQuery) error {
		f.stale = s.staleViews()
		cte, args := columnTrafficScope(f, false)
		return q.QueryRowContext(ctx, cte+"SELECT count(*) FROM traffic_selected", args...).Scan(&total)
	})
	return total, err
}

// ColumnTrafficRecent is one recorded response, without unrelated flow fields.
type ColumnTrafficRecent struct {
	Key          string
	At           time.Time
	Milliseconds uint64
}

// TrafficRecent returns the actual latest 60 samples, in chart time/key order.
// The same write-time latency projection used to select the keys supplies the
// response directly; loading full connection and DNS facts is unnecessary.
func (s *ColumnSnapshot) TrafficRecent(ctx context.Context, f ColumnTrafficFilter) ([]ColumnTrafficRecent, error) {
	var out []ColumnTrafficRecent
	err := s.WithRead(ctx, func(q ColumnQuery) error {
		f.stale = s.staleViews()
		cte, args := columnTrafficScope(f, true)
		rows, err := q.QueryContext(ctx, cte+"SELECT flow_key,start_ns,read_latency FROM traffic_selected ORDER BY start_ns DESC,flow_key DESC LIMIT 60", args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var point ColumnTrafficRecent
			var ns, latency int64
			if err = rows.Scan(&point.Key, &ns, &latency); err != nil {
				return err
			}
			point.At, point.Milliseconds = time.Unix(0, ns), uint64(latency)
			out = append(out, point)
		}
		return rows.Err()
	})
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, err
}

// staleViews reports that WithRead exposes this revision through UNION views
// (the writer has committed since), not plain tables. Call inside WithRead.
func (s *ColumnSnapshot) staleViews() bool { return s.revision != s.state.revision }
