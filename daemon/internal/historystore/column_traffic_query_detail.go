package historystore

import (
	"context"
	"database/sql/driver"
	"fmt"
	"strings"
	"time"

	"modernc.org/sqlite"
	"pax-connect/daemon/internal/config"
)

// ColumnTrafficDetailFact is the narrow input to existing detail presentation.
// A detail may return all connection keys, but only current-page/256 timing
// records carry full facts. These values are streamed, never retained by store.
type ColumnTrafficDetailFact struct {
	Key, ApplicationID, Application, Domain, Rule, Source, Direction, Route, RouteName, Trigger string
	Download                                                                                    uint64
	Host, IP, BytesState                                                                        string
	Port                                                                                        uint32
	Process, SourceKind, Transport, Failure                                                     string
	LatencyMS                                                                                   uint64
	LatencyKnown, Closed, StateUnknown                                                          bool
	StartedAt                                                                                   time.Time
	Upload                                                                                      uint64
}

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("pax_main_domain", 1, func(_ *sqlite.FunctionContext, a []driver.Value) (driver.Value, error) {
		v, _ := a[0].(string)
		return config.MainDomain(v), nil
	})
	sqlite.MustRegisterDeterministicScalarFunction("pax_traffic_contains", 2, func(_ *sqlite.FunctionContext, a []driver.Value) (driver.Value, error) {
		text, term := fmt.Sprint(a[0]), fmt.Sprint(a[1])
		if strings.Contains(strings.ToLower(text), strings.ToLower(term)) {
			return int64(1), nil
		}
		return int64(0), nil
	})
	sqlite.MustRegisterDeterministicScalarFunction("pax_traffic_trigger", 4, func(_ *sqlite.FunctionContext, a []driver.Value) (driver.Value, error) {
		v := make([]string, len(a))
		for i, x := range a {
			if x != nil {
				v[i] = fmt.Sprint(x)
			}
		}
		return config.TrafficTrigger(config.FlowFact{Application: v[0], Domain: v[1], IP: v[2], Rule: v[3]}), nil
	})
}

// EachTrafficDetailFact streams only fields necessarily returned by Basis and
// Related, after applying exactly the same frozen member selector as pages.
func (s *ColumnSnapshot) EachTrafficDetailFact(ctx context.Context, f ColumnTrafficFilter, fn func(ColumnTrafficDetailFact) error) error {
	return s.eachTrafficDetailFact(ctx, f, false, fn)
}

// EachTrafficTargetFact includes retained byte evidence and full target identity for aggregation.
func (s *ColumnSnapshot) EachTrafficTargetFact(ctx context.Context, f ColumnTrafficFilter, fn func(ColumnTrafficDetailFact) error) error {
	return s.eachTrafficDetailFact(ctx, f, true, fn)
}

func (s *ColumnSnapshot) eachTrafficDetailFact(ctx context.Context, f ColumnTrafficFilter, full bool, fn func(ColumnTrafficDetailFact) error) error {
	return s.WithRead(ctx, func(q ColumnQuery) error {
		f.stale = s.staleViews()
		cte, args := columnTrafficScope(f, false)
		query := cte + ` SELECT flow_key,CASE WHEN source_kind IN ('app','tool','system') THEN source_id ELSE application_id END,CASE WHEN source_kind IN ('app','tool','system') THEN source_name ELSE application END,pax_main_domain(domain),read_rule,read_rule_source,read_direction,read_route_id,read_route_name,pax_traffic_trigger(CASE WHEN source_kind IN ('app','tool','system') THEN source_name ELSE application END,domain,ip,read_rule),download,domain,ip,0,'',start_ns,0 FROM traffic_selected ORDER BY start_ns DESC,flow_key ASC`
		if full {
			// Probe each selected key directly, including each term of an older snapshot's UNION views.
			query = cte + ` SELECT s.flow_key,CASE WHEN s.source_kind IN ('app','tool','system') THEN s.source_id ELSE s.application_id END,CASE WHEN s.source_kind IN ('app','tool','system') THEN s.source_name ELSE s.application END,pax_main_domain(s.domain),s.read_rule,s.read_rule_source,s.read_direction,s.read_route_id,s.read_route_name,'',s.download,s.domain,s.ip,coalesce((SELECT port FROM snap_col_identities WHERE id=(SELECT identity_id FROM snap_col_connections WHERE flow_key=s.flow_key)),0),coalesce((SELECT bytes_state FROM snap_col_connections WHERE flow_key=s.flow_key),''),s.start_ns,coalesce((SELECT upload FROM snap_col_connections WHERE flow_key=s.flow_key),0) FROM traffic_selected s ORDER BY s.start_ns DESC,s.flow_key ASC`
		}
		if full {
			// Scalar probes keep older UNION snapshots keyed; do not materialize all facts by join.
			query = strings.Replace(query, " FROM traffic_selected s ORDER BY", `,
        s.process,s.source_kind,
        coalesce((SELECT transport FROM snap_col_identities WHERE id=(SELECT identity_id FROM snap_col_connections WHERE flow_key=s.flow_key)),''),
        s.read_failure,s.read_latency,s.read_latency_known,
        CASE WHEN s.read_future=0 AND s.closed_ns IS NOT NULL THEN 1 ELSE 0 END,
        CASE WHEN s.read_future=1 THEN 1 ELSE coalesce((SELECT state_unknown FROM snap_col_connections WHERE flow_key=s.flow_key),0) END
        FROM traffic_selected s ORDER BY`, 1)
		} else {
			query = strings.Replace(query, " FROM traffic_selected ORDER BY", ",'','','','',0,0,0,0 FROM traffic_selected ORDER BY", 1)
		}
		rows, err := q.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var v ColumnTrafficDetailFact
			var down, up, started, latency int64
			if err = rows.Scan(&v.Key, &v.ApplicationID, &v.Application, &v.Domain, &v.Rule, &v.Source, &v.Direction, &v.Route, &v.RouteName, &v.Trigger, &down, &v.Host, &v.IP, &v.Port, &v.BytesState, &started, &up, &v.Process, &v.SourceKind, &v.Transport, &v.Failure, &latency, &v.LatencyKnown, &v.Closed, &v.StateUnknown); err != nil {
				return err
			}
			v.Download = uint64(down)
			v.Upload = uint64(up)
			v.LatencyMS = uint64(latency)
			v.StartedAt = time.Unix(0, started)
			if err = fn(v); err != nil {
				return err
			}
		}
		return rows.Err()
	})
}

// TrafficTimings selects the same earliest entries of the DESC/key-ASC detail
// traversal that production uses, bounded to 257 so truncation remains exact.
func (s *ColumnSnapshot) TrafficTimings(ctx context.Context, f ColumnTrafficFilter) ([]config.LogConnectionSummary, error) {
	f.Buckets = nil // detail timings use all retained members, unlike recent charts
	var out []config.LogConnectionSummary
	err := s.WithRead(ctx, func(q ColumnQuery) error {
		f.stale = s.staleViews()
		cte, args := columnTrafficScope(f, true)
		var err error
		keys, e := columnTrafficKeys(ctx, q, cte+"SELECT flow_key FROM traffic_selected ORDER BY start_ns DESC,flow_key ASC LIMIT 257", args)
		if e != nil {
			return e
		}
		out, err = columnTrafficFetchKeys(ctx, q, f, keys)
		return err
	})
	return out, err
}

// TrafficConnectionKeys returns a bounded page already selected by the frozen detail projection.
func (s *ColumnSnapshot) TrafficConnectionKeys(ctx context.Context, f ColumnTrafficFilter, keys []string) ([]config.LogConnectionSummary, error) {
	if len(keys) > 200 {
		return nil, fmt.Errorf("historystore: invalid traffic page")
	}
	var out []config.LogConnectionSummary
	err := s.WithRead(ctx, func(q ColumnQuery) error {
		f.stale = s.staleViews()
		var err error
		out, err = columnTrafficFetchKeys(ctx, q, f, keys)
		return err
	})
	return out, err
}
