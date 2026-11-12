package aiusage

import "context"

// Analysis reads indexed presentation metadata; source I/O belongs to collection.
func (s *Store) fillSessionTitles(ctx context.Context, out *Analysis) {
	ids := make(map[SessionKey]bool)
	for _, row := range out.Rows {
		ids[SessionKey{row.CLI, row.Session}] = true
	}
	rows, err := s.r.QueryContext(ctx, `SELECT cli,session,title FROM session_catalog`)
	if err != nil {
		return
	}
	defer func() { _ = rows.Close() }()
	titles := make(map[SessionKey]string)
	for rows.Next() {
		var k SessionKey
		var title string
		if rows.Scan(&k.CLI, &k.Session, &title) != nil {
			return
		}
		if ids[k] {
			titles[k] = title
		}
	}
	if rows.Err() != nil {
		return
	}
	for i := range out.Rows {
		out.Rows[i].SessionTitle = titles[SessionKey{out.Rows[i].CLI, out.Rows[i].Session}]
	}
	for i := range out.Requests {
		out.Requests[i].SessionTitle = titles[SessionKey{out.Requests[i].CLI, out.Requests[i].Session}]
	}
}
