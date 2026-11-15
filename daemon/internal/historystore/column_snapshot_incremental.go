package historystore

import (
	"context"

	"pax-connect/daemon/internal/config"
)

// EventsAfter is the production incremental interface: standard events cross
// this boundary directly, without legacy Body serialization.
func (s *ColumnSnapshot) EventsAfter(ctx context.Context, after, head int64, limit int) ([]config.HistoryEvent, error) {
	var out []config.HistoryEvent
	err := s.WithRead(ctx, func(q ColumnQuery) error {
		r := snapshotQuery{q}
		rows, err := r.QueryContext(ctx, columnEventSelect()+" WHERE e.seq>? AND e.seq<=? ORDER BY e.seq LIMIT ?", after, head, limit)
		if err != nil {
			return err
		}
		for rows.Next() {
			e, er := scanColumnEvent(rows)
			if er != nil {
				_ = rows.Close()
				return er
			}
			e.Raw = nil
			out = append(out, e)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
		for i := range out {
			if err = columnEventChildren(ctx, r, &out[i]); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}
