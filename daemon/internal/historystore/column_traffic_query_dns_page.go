package historystore

import (
	"context"
	"fmt"

	"pax-connect/daemon/internal/config"
)

// ColumnTrafficDNSPage materializes only keys/times/group IDs/latencies for a
// single RPC. Full evidence is fetched for its output page and latest samples.
type ColumnTrafficDNSPage struct {
	GroupTotal, ConnectionTotal int
	Groups                      []ColumnTrafficDNSGroup
	Items, Recent               []config.LogConnectionSummary
}

// TrafficDNSPage resolves directory, subgroup page and recent chart in one
// short snapshot read, rather than rescanning the complete DNS scope per field.
func (s *ColumnSnapshot) TrafficDNSPage(ctx context.Context, f ColumnTrafficFilter, offset, limit int) (ColumnTrafficDNSPage, error) {
	var out ColumnTrafficDNSPage
	if offset < 0 || limit < 1 || limit > 100 {
		return out, fmt.Errorf("historystore: invalid DNS page")
	}
	group := f.DNSGroupID
	f.DNSGroupID = ""
	err := s.WithRead(ctx, func(q ColumnQuery) error {
		f.stale = s.staleViews()
		cte, args := columnTrafficScope(f, false)
		if _, err := q.ExecContext(ctx, "DROP TABLE IF EXISTS temp.traffic_dns"); err != nil {
			return err
		}
		query := "CREATE TEMP TABLE traffic_dns AS " + cte + `SELECT flow_key,start_ns,read_latency_known,read_latency,dns_group_id AS group_id FROM traffic_selected`
		if _, err := q.ExecContext(ctx, query, args...); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, "CREATE INDEX temp.traffic_dns_groups ON traffic_dns(group_id,start_ns DESC,flow_key ASC)"); err != nil {
			return err
		}
		if err := q.QueryRowContext(ctx, "SELECT count(DISTINCT group_id) FROM traffic_dns").Scan(&out.GroupTotal); err != nil {
			return err
		}
		where := "1"
		var a []any
		if group != "" {
			where = "group_id=?"
			a = []any{group}
		}
		if err := q.QueryRowContext(ctx, "SELECT count(*) FROM traffic_dns WHERE "+where, a...).Scan(&out.ConnectionTotal); err != nil {
			return err
		}
		fetch := func(keys []string) ([]config.LogConnectionSummary, error) {
			return columnTrafficFetchKeys(ctx, q, f, keys)
		}
		keys := func(stmt string, args ...any) ([]string, error) {
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
		if group != "" {
			selected, err := keys("SELECT flow_key FROM traffic_dns WHERE group_id=? ORDER BY start_ns DESC,flow_key ASC LIMIT ? OFFSET ?", group, limit, offset)
			if err != nil {
				return err
			}
			out.Items, err = fetch(selected)
			if err != nil {
				return err
			}
		}
		groupQuery := `WITH ranked AS (SELECT group_id,flow_key,start_ns,count(*) OVER(PARTITION BY group_id) AS queries,row_number() OVER(PARTITION BY group_id ORDER BY start_ns DESC,flow_key ASC) AS ordinal FROM traffic_dns WHERE ` + where + `) SELECT group_id,flow_key,queries FROM ranked WHERE ordinal=1 ORDER BY start_ns DESC,group_id LIMIT ? OFFSET ?`
		gLimit, gOffset := limit, offset
		if group != "" {
			gLimit, gOffset = 1, 0
		}
		rows, err := q.QueryContext(ctx, groupQuery, append(append([]any{}, a...), gLimit, gOffset)...)
		if err != nil {
			return err
		}
		type selectedGroup struct {
			id, key string
			n       uint64
		}
		var gs []selectedGroup
		for rows.Next() {
			var v selectedGroup
			if err = rows.Scan(&v.id, &v.key, &v.n); err != nil {
				_ = rows.Close()
				return err
			}
			gs = append(gs, v)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
		var groupKeys []string
		for _, g := range gs {
			groupKeys = append(groupKeys, g.key)
		}
		latest, err := fetch(groupKeys)
		if err != nil {
			return err
		}
		for i, g := range gs {
			out.Groups = append(out.Groups, ColumnTrafficDNSGroup{ID: g.id, Queries: g.n, Latest: latest[i]})
		}
		recent, err := keys("SELECT flow_key FROM traffic_dns WHERE "+where+" AND read_latency_known=1 ORDER BY start_ns DESC,flow_key DESC LIMIT 60", a...)
		if err != nil {
			return err
		}
		out.Recent, err = fetch(recent)
		if err != nil {
			return err
		}
		for i, j := 0, len(out.Recent)-1; i < j; i, j = i+1, j-1 {
			out.Recent[i], out.Recent[j] = out.Recent[j], out.Recent[i]
		}
		return nil
	})
	return out, err
}
