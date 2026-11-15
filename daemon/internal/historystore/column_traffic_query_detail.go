package historystore

import (
	"context"
	"database/sql/driver"
	"fmt"
	"strings"

	"modernc.org/sqlite"
	"pax-connect/daemon/internal/config"
)

// ColumnTrafficDetailFact is the narrow input to existing detail presentation.
// A detail may return all connection keys, but only current-page/256 timing
// records carry full facts. These values are streamed, never retained by store.
type ColumnTrafficDetailFact struct {
	Key, ApplicationID, Application, Domain, Rule, Source, Direction, Route, RouteName, Trigger string
	Download                                                                                    uint64
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
	return s.WithRead(ctx, func(q ColumnQuery) error {
		f.stale = s.staleViews()
		cte, args := columnTrafficScope(f, false)
		query := cte + ` SELECT flow_key,CASE WHEN source_kind IN ('app','tool','system') THEN source_id ELSE application_id END,CASE WHEN source_kind IN ('app','tool','system') THEN source_name ELSE application END,pax_main_domain(domain),read_rule,read_rule_source,read_direction,read_route_id,read_route_name,pax_traffic_trigger(CASE WHEN source_kind IN ('app','tool','system') THEN source_name ELSE application END,domain,ip,read_rule),download FROM traffic_selected ORDER BY start_ns DESC,flow_key ASC`
		rows, err := q.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var v ColumnTrafficDetailFact
			var down int64
			if err = rows.Scan(&v.Key, &v.ApplicationID, &v.Application, &v.Domain, &v.Rule, &v.Source, &v.Direction, &v.Route, &v.RouteName, &v.Trigger, &down); err != nil {
				return err
			}
			v.Download = uint64(down)
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
