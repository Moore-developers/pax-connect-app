package historystore

import "context"

// RoutingCandidateBytes is the recorded traffic for one retained app/path pair.
type RoutingCandidateBytes struct {
	ApplicationID, Process string
	Bytes                  uint64
	LastActivityMs         int64
}

// RoutingCandidateTraffic sums the recorded bytes of connections active within the selected log window.
func (l *Logs) RoutingCandidateTraffic(ctx context.Context, fromMs, nowMs int64) ([]RoutingCandidateBytes, error) {
	rows, err := l.r.QueryContext(ctx, `
SELECT coalesce(i.application_id,''),coalesce(i.process,''),
       sum(max(0,coalesce(c.upload,0))+max(0,coalesce(c.download,0))),max(c.last_activity_ms)
FROM col_connections c JOIN col_identities i ON i.id=c.identity_id
WHERE c.last_activity_ms>? AND c.last_activity_ms<=?
  AND (c.upload>0 OR c.download>0)
  AND NOT EXISTS(SELECT 1 FROM col_traffic t WHERE t.flow_key=c.flow_key AND t.source_kind='dns')
GROUP BY i.application_id,i.process`, fromMs, nowMs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var traffic []RoutingCandidateBytes
	for rows.Next() {
		var row RoutingCandidateBytes
		if err = rows.Scan(&row.ApplicationID, &row.Process, &row.Bytes, &row.LastActivityMs); err != nil {
			return nil, err
		}
		traffic = append(traffic, row)
	}
	return traffic, rows.Err()
}
