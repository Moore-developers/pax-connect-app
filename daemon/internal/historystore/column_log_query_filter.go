package historystore

import (
	"context"
	"database/sql/driver"
	"fmt"
	"net/netip"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"modernc.org/sqlite"
	"pax-connect/daemon/internal/config"
)

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("column_lower", 1, func(_ *sqlite.FunctionContext, a []driver.Value) (driver.Value, error) {
		v, _ := a[0].(string)
		return strings.ToLower(v), nil
	})
	sqlite.MustRegisterDeterministicScalarFunction("column_uint_ge", 2, func(_ *sqlite.FunctionContext, a []driver.Value) (driver.Value, error) {
		n, _ := a[0].(int64)
		text, _ := a[1].(string)
		threshold, err := strconv.ParseUint(text, 10, 64)
		if err != nil {
			return nil, err
		}
		if uint64(n) >= threshold {
			return int64(1), nil
		}
		return int64(0), nil
	})
	sqlite.MustRegisterScalarFunction("column_main_domain", 1, func(_ *sqlite.FunctionContext, a []driver.Value) (driver.Value, error) {
		v, _ := a[0].(string)
		return config.MainDomain(v), nil
	})
	sqlite.MustRegisterScalarFunction("column_normal_ip", 1, func(_ *sqlite.FunctionContext, a []driver.Value) (driver.Value, error) {
		v, _ := a[0].(string)
		ip, e := netip.ParseAddr(strings.TrimSpace(v))
		if e != nil {
			return "", nil
		}
		return ip.Unmap().String(), nil
	})
	sqlite.MustRegisterScalarFunction("column_ip_matches", 2, func(_ *sqlite.FunctionContext, a []driver.Value) (driver.Value, error) {
		v, _ := a[0].(string)
		f, _ := a[1].(string)
		ip, e := netip.ParseAddr(v)
		if e != nil {
			return int64(0), nil
		}
		ip = ip.Unmap()
		ok := false
		if p, e := netip.ParsePrefix(f); e == nil {
			ok = p.Contains(ip)
		} else if p, e := netip.ParseAddr(f); e == nil {
			ok = p.Unmap() == ip
		}
		if ok {
			return int64(1), nil
		}
		return int64(0), nil
	})
	sqlite.MustRegisterScalarFunction("column_process_name", 1, func(_ *sqlite.FunctionContext, a []driver.Value) (driver.Value, error) {
		v, _ := a[0].(string)
		if v == "" {
			return "", nil
		}
		return filepath.Base(v), nil
	})

	sqlite.MustRegisterScalarFunction("column_app_name", 3, func(_ *sqlite.FunctionContext, a []driver.Value) (driver.Value, error) {
		id, _ := a[0].(string)
		name, _ := a[1].(string)
		process, _ := a[2].(string)
		return config.FlowApplicationName(config.FlowFact{ApplicationID: id, Application: name, Process: process}), nil
	})
}

func bindLogSQL(q string, args []any) string {
	for _, v := range args {
		var text string
		switch x := v.(type) {
		case string:
			text = "'" + strings.ReplaceAll(x, "'", "''") + "'"
		case int64:
			text = strconv.FormatInt(x, 10)
		default:
			panic(fmt.Sprintf("internal log SQL literal %T", v))
		}
		q = strings.Replace(q, "?", text, 1)
	}
	return q
}

func (s *ColumnLogSelection) catalog(ctx context.Context) error {
	var source string
	if s.Window.View == "" {
		source = `SELECT e.event_id AS k,i.flow_key AS flow_key,e.event_id,e.at_ns AS last_ns,CASE e.level WHEN 'error' THEN 3 WHEN 'warning' THEN 2 ELSE 1 END AS level,
e.flow_present AS flow,i.application_id AS app,column_app_name(i.application_id,i.application,i.process) AS app_name,i.process,i.domain,i.ip,i.route_id AS route,i.route_name,i.direction,e.source,e.kind AS record_kind,
e.phase,e.started_at,e.closed_at,e.observed_until,e.response_known,e.response_ms,e.state_unknown,e.establishment_state,e.establishment_ms,e.failure,e.evidence,e.dns_type,e.dns_result,i.purpose,i.transport,i.port,
x.rule_package,x.latency_known,x.latency,x.system_category,'' AS category_name,'' AS category_symbol,'' AS metrics_asof,0 AS metrics_present,0 AS user_app,'' AS user_app_id,'' AS user_app_name,
0 AS answers_kind,e.event_id AS answers_key FROM snap_col_events e JOIN snap_col_identities i ON i.id=e.identity_id JOIN snap_col_event_index x ON x.event_id=e.event_id WHERE e.at_ns>` + strconv.FormatInt(columnQueryTime(s.Window.From), 10) + ` AND e.at_ns>=` + strconv.FormatInt(columnQueryTime(s.Window.Cutoff), 10) + ` AND e.at_ns<=` + strconv.FormatInt(columnQueryTime(s.Window.Until), 10)
		if s.Window.Route != "" {
			source += " AND i.route_id=" + bindLogSQL("?", []any{s.Window.Route})
		}
	} else {
		source = `SELECT g.group_key AS k,i.flow_key AS flow_key,g.event_id,g.last_ns,g.level,g.flow_present AS flow,i.application_id AS app,i.application AS app_name,i.process,i.domain,i.ip,i.route_id AS route,i.route_name,i.direction,e.source,g.record_kind,
g.phase,g.started_at,g.closed_at,g.observed_until,g.response_known,g.response_ms,g.state_unknown,g.establishment_state,g.establishment_ms,g.failure,g.evidence,g.dns_type,g.dns_result,i.purpose,i.transport,i.port,
g.rule_package,g.latency_known,g.latency,g.system_category,g.category_name,g.category_symbol,g.as_of AS metrics_asof,g.metrics_present,CASE WHEN ua.id IS NULL THEN 0 ELSE 1 END AS user_app,coalesce(ua.id,'') AS user_app_id,coalesce(ua.name,'') AS user_app_name,
2 AS answers_kind,g.view||char(0)||g.group_key AS answers_key FROM log_groups g JOIN log_identities i ON i.id=g.identity_id JOIN snap_col_events e ON e.event_id=g.event_id LEFT JOIN log_app_matches ua ON ua.app_id=i.application_id AND ua.process=i.process`
	}
	if s.Window.View != "" && s.Window.View != "activity" {
		source = strings.Replace(source, "i.application_id AS app,i.application AS app_name,i.process,i.domain,i.ip,i.route_id AS route,i.route_name,i.direction", "g.app_id AS app,g.app_name AS app_name,i.process,g.domain,g.ip,g.route_id AS route,g.route_name,g.direction", 1)
	}
	if s.Window.View != "" {
		// Keep each source narrow before UNION: joining the wide group UNION
		// first makes SQLite materialize every scalar/flow column on each query.
		boundary := strings.Index(source, " FROM log_groups g JOIN log_identities")
		columns, join := source[len("SELECT "):boundary], source[boundary:]
		bare := regexp.MustCompile(`\bi\.(` + strings.ReplaceAll(identityColumnNames, ",", "|") + `)([,\n])`)
		columns = bare.ReplaceAllString(columns, `i.$1 AS $1$2`)
		completeJoin := strings.Replace(join, " FROM log_groups g JOIN log_identities i ON i.id=g.identity_id", " FROM snap_col_groups g JOIN snap_col_identities i ON i.id=g.identity_id", 1)
		deltaJoin := strings.Replace(join, " FROM log_groups g JOIN log_identities i ON i.id=g.identity_id", " FROM log_delta g JOIN log_identity_delta i ON i.id=g.identity_id", 1)
		predicate := s.completeSQL[strings.Index(s.completeSQL, " WHERE ")+len(" WHERE "):]
		if s.Window.Route != "" || s.Window.ConnectionKey != "" {
			completeJoin = strings.Replace(completeJoin, " FROM snap_col_groups g ", " FROM snap_col_groups g JOIN log_scope t ON t.k=g.group_key ", 1)
		}
		source = "SELECT " + columns + completeJoin + " WHERE " + predicate + " UNION ALL SELECT " + columns + deltaJoin
	}

	_, err := s.q.ExecContext(ctx, "CREATE TEMP VIEW log_catalog AS "+source)
	return err
}

// ColumnLogFilter contains product query values, not SQL or protocol types.
type ColumnLogFilter struct {
	Dimension, ObjectID, Level, Scope, Quick, ConnectionKey, ProblemID         string
	IncludeDNS                                                                 bool
	Kinds, Levels, Transports, Directions                                      []string
	Application, Domain, DomainMode, TargetIP, Route, RulePackage, DNSAnswerIP string
	Port                                                                       uint32
	MinLatency                                                                 uint64
}

func (s *ColumnLogSelection) ObjectExpressions(dimension string) (string, string, string) {
	if s.Window.View != "activity" {
		switch dimension {
		case "application":
			id, name := "CASE WHEN app<>'' THEN app ELSE 'unknown' END", "CASE WHEN app<>'' THEN app_name ELSE '未记录' END"
			if s.Window.View != "" {
				id = "CASE WHEN source='Pax' THEN 'pax-connect' ELSE " + id + " END"
				name = "CASE WHEN source='Pax' THEN 'Pax Connect' ELSE " + name + " END"
			}
			return id, name, "''"
		case "ip":
			return "CASE WHEN ip<>'' THEN ip ELSE 'unknown' END", "CASE WHEN ip<>'' THEN ip ELSE '未记录' END", "''"
		default:
			return "CASE WHEN column_main_domain(domain)<>'' THEN column_main_domain(domain) ELSE 'unknown' END", "CASE WHEN column_main_domain(domain)<>'' THEN column_main_domain(domain) ELSE '未记录' END", "''"
		}
	}
	if dimension == "system" {
		id := `CASE WHEN flow=0 THEN system_category WHEN purpose='dns' THEN 'network/dns' WHEN process<>'' THEN 'network/process:'||process WHEN app<>'' THEN 'network/'||app ELSE 'network/unknown' END`
		name := `CASE WHEN flow=0 THEN CASE WHEN category_name<>'' THEN category_name ELSE source||'事件' END WHEN purpose='dns' THEN '域名解析' WHEN app_name<>'' THEN app_name WHEN process<>'' THEN column_process_name(process) ELSE '未识别进程' END`
		return id, name, `CASE WHEN flow=1 THEN CASE WHEN user_app=1 THEN 'app' ELSE 'gearshape' END ELSE category_symbol END`
	}
	var id, name string
	switch dimension {
	case "application":
		valid := `trim(app_name)<>'' AND trim(app_name) NOT IN ('unknown','未识别应用','未识别的应用')`
		id = `CASE WHEN user_app=1 THEN user_app_id WHEN ` + valid + ` THEN CASE WHEN app<>'' THEN app ELSE trim(app_name) END ELSE 'unknown' END`
		name = `CASE WHEN user_app=1 THEN user_app_name WHEN ` + valid + ` THEN trim(app_name) ELSE '未记录' END`
	case "ip":
		id = `CASE WHEN column_normal_ip(ip)<>'' THEN column_normal_ip(ip) ELSE 'unknown' END`
		name = `CASE WHEN column_normal_ip(ip)<>'' THEN column_normal_ip(ip) ELSE '未记录' END`
	default:
		id = `CASE WHEN column_main_domain(domain)<>'' THEN column_main_domain(domain) ELSE 'unknown' END`
		name = `CASE WHEN column_main_domain(domain)<>'' THEN column_main_domain(domain) ELSE '未记录' END`
	}
	return id, name, `CASE WHEN flow=1 THEN CASE WHEN user_app=1 THEN 'app' ELSE 'gearshape' END ELSE category_symbol END`
}

func (s *ColumnLogSelection) where(f ColumnLogFilter) (string, []any) {
	var clauses []string
	var args []any
	add := func(q string, a ...any) { clauses = append(clauses, q); args = append(args, a...) }
	if f.Level != "" {
		add("level=?", map[string]int{"info": 1, "warning": 2, "error": 3}[f.Level])
	}
	if f.ObjectID != "" && f.ObjectID != "all" {
		id, _, _ := s.ObjectExpressions(f.Dimension)
		add(id+"=?", f.ObjectID)
	}
	if s.Window.View != "" && f.ConnectionKey != "" {
		add("flow=1 AND flow_key=?", f.ConnectionKey)
	}
	if s.Window.View == "problems" && f.ProblemID != "" {
		add("k=?", f.ProblemID)
	}
	if s.Window.View == "access" && f.ProblemID != "" {
		query := `k IN(SELECT ax.access FROM snap_col_event_index px JOIN snap_col_event_index ax ON ax.flow_key=px.flow_key WHERE px.problem<>'' AND px.problem=? AND ax.access<>'' AND px.at_ns>? AND px.at_ns>=? AND px.at_ns<=?`
		a := []any{f.ProblemID, columnQueryTime(s.Window.From), columnQueryTime(s.Window.Cutoff), columnQueryTime(s.Window.Until)}
		if s.Window.Route != "" {
			query += " AND px.route=?"
			a = append(a, s.Window.Route)
		}
		add(query+")", a...)
	}
	if s.Window.View == "activity" {
		if f.Scope == "system" || f.Dimension == "system" {
			if f.Scope == "" {
				add("flow=0")
			} else {
				add("NOT(flow=1 AND user_app=1)")
			}
		}
		if f.Scope == "access" {
			add("flow=1")
			if f.Dimension == "application" {
				add("user_app=1")
			}
		}
		dnsOnly := len(f.Kinds) == 1 && f.Kinds[0] == "dns"
		if !f.IncludeDNS && !dnsOnly && f.ConnectionKey == "" && f.Scope != "system" {
			add("NOT(flow=1 AND purpose='dns' AND level=1)")
		}
		kind := `CASE WHEN flow=0 THEN record_kind WHEN purpose='dns' THEN 'dns' ELSE 'access' END`
		switch f.Quick {
		case "problems":
			state := "state_unknown=1"
			if s.Window.Collection != "collecting" {
				state = "(state_unknown=1 OR (closed_at IS NULL AND metrics_present=1 AND metrics_asof<>'0001-01-01T00:00:00Z'))"
			}
			add("(level IN(2,3) OR (flow=1 AND " + state + "))")
		case "blocked":
			add("flow=1 AND (direction='blocked' OR failure='rule_rejected')")
		case "slow":
			n := f.MinLatency
			if n == 0 {
				n = 1000
			}
			add(kind+"='access' AND latency_known=1 AND column_uint_ge(latency,?)=1", strconv.FormatUint(n, 10))
		}
		for _, x := range []struct {
			col    string
			values []string
		}{{kind, f.Kinds}, {"transport", f.Transports}, {"direction", f.Directions}} {
			if len(x.values) > 0 {
				a := make([]any, len(x.values))
				for i, v := range x.values {
					a[i] = v
				}
				add(x.col+" IN("+strings.TrimSuffix(strings.Repeat("?,", len(a)), ",")+")", a...)
			}
		}
		if len(f.Levels) > 0 {
			a := make([]any, len(f.Levels))
			for i, v := range f.Levels {
				a[i] = map[string]int{"info": 1, "warning": 2, "error": 3}[v]
			}
			add("level IN("+strings.TrimSuffix(strings.Repeat("?,", len(a)), ",")+")", a...)
		}
		if f.Application != "" {
			add("flow=1 AND instr(column_lower(app_name||' '||app),?)>0", strings.ToLower(f.Application))
		}
		if f.Domain != "" {
			host := "column_lower(rtrim(domain,'.'))"
			target := strings.TrimSuffix(strings.ToLower(f.Domain), ".")
			switch f.DomainMode {
			case "exact":
				add("flow=1 AND "+host+"=?", target)
			case "suffix":
				add("flow=1 AND ("+host+"=? OR substr("+host+",-?)=?)", target, len(target)+1, "."+target)
			default:
				add("flow=1 AND instr("+host+",?)>0", target)
			}
		}
		if f.TargetIP != "" {
			add(kind+"<>'dns' AND flow=1 AND column_ip_matches(ip,?)=1", f.TargetIP)
		}
		if f.Port > 0 {
			add("flow=1 AND port=?", f.Port)
		}
		if f.Route != "" {
			add("flow=1 AND (route=? OR route_name=?)", f.Route, f.Route)
		}
		if f.RulePackage != "" {
			add("flow=1 AND rule_package=?", f.RulePackage)
		}
		if f.MinLatency > 0 {
			add(kind+"='access' AND latency_known=1 AND column_uint_ge(latency,?)=1", strconv.FormatUint(f.MinLatency, 10))
		}
		if f.DNSAnswerIP != "" {
			add(kind+"='dns' AND EXISTS(SELECT 1 FROM log_answers a WHERE a.owner_kind=l.answers_kind AND a.owner_key=l.answers_key AND column_ip_matches(a.value,?)=1)", f.DNSAnswerIP)
		}
	}
	if len(clauses) == 0 {
		return "1", nil
	}
	return strings.Join(clauses, " AND "), args
}
