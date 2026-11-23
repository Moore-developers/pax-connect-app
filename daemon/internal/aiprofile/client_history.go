package aiprofile

import "context"

// HistoryCursor identifies the last visible event in a newest-first page.
type HistoryCursor struct {
	At, RowID int64
}

// ClientHistoryEvent describes a recorded configuration change, without file
// paths, endpoints, credentials or billing metadata.
type ClientHistoryEvent struct {
	At                                          int64
	Kind, ProfileID, ProfileName, ServiceSource string
}

// ListClientHistory reads committed changes for a configurable client. Seeds
// and multiplier edits serve attribution only and are not configuration events.
func (m *Manager) ListClientHistory(ctx context.Context, cli CLI, before HistoryCursor) ([]ClientHistoryEvent, HistoryCursor, error) {
	if cli != Claude && cli != Codex || (before.At == 0) != (before.RowID == 0) || before.At < 0 || before.RowID < 0 {
		return nil, HistoryCursor{}, Invalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	const limit = 50
	rows, err := m.events.QueryContext(ctx, `
		SELECT at,rowid,kind,profile_id,profile_name,service_source FROM profile_events
		WHERE cli=? AND kind IN ('apply','observe') AND event_key NOT LIKE 'seed:%'
		AND (?=0 OR (at,rowid)<(?,?))
		ORDER BY at DESC,rowid DESC LIMIT ?`, cli, before.At, before.At, before.RowID, limit+1)
	if err != nil {
		return nil, HistoryCursor{}, cause(err)
	}
	defer func() { _ = rows.Close() }()
	var events []ClientHistoryEvent
	var last HistoryCursor
	for rows.Next() {
		if len(events) == limit {
			return events, last, nil
		}
		var item ClientHistoryEvent
		if err := rows.Scan(&item.At, &last.RowID, &item.Kind, &item.ProfileID, &item.ProfileName, &item.ServiceSource); err != nil {
			return nil, HistoryCursor{}, cause(err)
		}
		last.At = item.At
		events = append(events, item)
	}
	return events, HistoryCursor{}, cause(rows.Err())
}
