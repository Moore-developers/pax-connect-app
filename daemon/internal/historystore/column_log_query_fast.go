package historystore

import (
	"context"
	"strings"
)

// basicActivity handles the actual unadvanced UI selection with only its SQL
// filter columns. Full row fields are fetched after current-page keys are fixed.
func (s *ColumnLogSelection) basicActivity(f ColumnLogFilter) bool {
	objectSupported := f.ObjectID == "" || f.ObjectID == "all" || s.Window.ApplicationPage
	return s.Window.View == "activity" && objectSupported && f.ConnectionKey == "" && f.ProblemID == "" && (f.Quick == "" || f.Quick == "all") && len(f.Kinds) == 0 && len(f.Levels) == 0 && len(f.Transports) == 0 && len(f.Directions) == 0 && f.Application == "" && f.Domain == "" && f.TargetIP == "" && f.Route == "" && f.RulePackage == "" && f.DNSAnswerIP == "" && f.Port == 0 && f.MinLatency == 0
}

// basicDomainObjectSource serves the ordinary access directory from the
// already-frozen group and identity fields. It does not need event bodies or
// the wide catalog used by advanced filters.
func (s *ColumnLogSelection) basicDomainObjectSource(f ColumnLogFilter) (string, bool) {
	if !s.basicActivity(f) || f.Scope != "access" || f.Dimension != "" && f.Dimension != "domain" || f.Level != "" || f.IncludeDNS || s.Window.Route != "" {
		return "", false
	}
	cols := "g.group_key,g.last_ns,g.level,g.flow_present,g.identity_id,g.source_kind,g.primary_domain"
	selectRows := func(groups string) string {
		if s.deferredDomainClassification {
			return `SELECT g.primary_domain AS root,g.last_ns,g.group_key AS k,g.identity_id AS idref,0 AS user_app FROM ` + groups + ` g WHERE g.flow_present=1 AND NOT(g.source_kind='dns' AND g.level=1)`
		}
		return `SELECT g.primary_domain AS root,g.last_ns,g.group_key AS k,g.identity_id AS idref,CASE WHEN m.id IS NULL THEN 0 ELSE 1 END AS user_app FROM ` + groups + ` g LEFT JOIN log_identity_matches m ON m.id=g.identity_id WHERE g.flow_present=1 AND NOT(g.source_kind='dns' AND g.level=1)`
	}
	rows := selectRows("("+s.completeProjection(cols)+")") + " UNION ALL " + selectRows("log_delta")
	return `SELECT CASE WHEN root<>'' THEN root ELSE 'unknown' END AS id,CASE WHEN root<>'' THEN root ELSE '未记录' END AS name,CASE WHEN user_app=1 THEN 'app' ELSE 'gearshape' END AS symbol,last_ns,k,idref FROM (` + rows + `)`, true
}
func (s *ColumnLogSelection) basicActivitySQL(f ColumnLogFilter) (string, string, []any) {
	cols := "g.group_key,g.last_ns,g.level,g.flow_present,g.identity_id,g.source_kind"
	base := s.completeProjection(cols)
	union := base + " UNION ALL SELECT group_key,last_ns,level,flow_present,identity_id,source_kind FROM log_delta"
	source := "(" + union + ") g"
	var clauses []string
	var args []any
	if f.Level != "" {
		clauses = append(clauses, "g.level=?")
		args = append(args, map[string]int{"info": 1, "warning": 2, "error": 3}[f.Level])
	}
	if f.Scope == "access" {
		clauses = append(clauses, "g.flow_present=1")
		if f.Dimension == "application" {
			if f.ObjectID == "" || f.ObjectID == "all" {
				clauses = append(clauses, "EXISTS(SELECT 1 FROM log_identity_matches m WHERE m.id=g.identity_id)")
			} else {
				// Keep the same installed-app match, including path-owned helper processes.
				clauses = append(clauses, `g.identity_id IN(
SELECT i.id FROM log_identities i
WHERE (i.application_id,i.process) IN(SELECT app_id,process FROM log_app_matches WHERE id=?))`)
				args = append(args, f.ObjectID)
			}
		}
	}
	if f.Scope == "system" || f.Dimension == "system" {
		if f.Scope == "" {
			clauses = append(clauses, "g.flow_present=0")
		} else {
			clauses = append(clauses, "NOT(g.flow_present=1 AND EXISTS(SELECT 1 FROM log_identity_matches m WHERE m.id=g.identity_id))")
		}
	}
	if !f.IncludeDNS && f.Scope != "system" {
		clauses = append(clauses, "NOT(g.flow_present=1 AND g.source_kind='dns' AND g.level=1)")
	}
	where := "1"
	if len(clauses) > 0 {
		where = strings.Join(clauses, " AND ")
	}
	return source, where, args
}

func (s *ColumnLogSelection) basicActivityCount(ctx context.Context, f ColumnLogFilter) (uint64, error) {
	source, where, args := s.basicActivitySQL(f)
	var n uint64
	err := s.q.QueryRowContext(ctx, "SELECT count(*) FROM "+source+" WHERE "+where, args...).Scan(&n)
	return n, err
}
func (s *ColumnLogSelection) basicActivityPage(ctx context.Context, f ColumnLogFilter, offset, limit int) ([]string, error) {
	keys, err := s.ActivityPage(ctx, f, nil, offset, limit)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key.Key)
	}
	return out, err
}

// ActivityPageKey identifies a row in the snapshot's descending sort order.
type ActivityPageKey struct {
	LastNS int64
	Key    string
}

// ActivityPage supports an index seek after an existing row, including ties.
func (s *ColumnLogSelection) ActivityPage(ctx context.Context, f ColumnLogFilter, after *ActivityPageKey, offset, limit int) ([]ActivityPageKey, error) {
	source, where, args := s.basicActivitySQL(f)
	if after != nil {
		where += " AND (g.last_ns,g.group_key)<(?,?)"
		args = append(args, after.LastNS, after.Key)
	}
	rows, err := s.q.QueryContext(ctx, "SELECT g.group_key,g.last_ns FROM "+source+" WHERE "+where+" ORDER BY g.last_ns DESC,g.group_key DESC LIMIT ? OFFSET ?", append(args, limit, offset)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ActivityPageKey
	for rows.Next() {
		var key ActivityPageKey
		if err = rows.Scan(&key.Key, &key.LastNS); err != nil {
			return nil, err
		}
		out = append(out, key)
	}
	return out, rows.Err()
}
