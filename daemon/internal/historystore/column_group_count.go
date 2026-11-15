package historystore

import "context"

// GroupEventCount proves whether a newly written batch contains all retained
// events of a group. The explicit partial-index predicate is intentional.
func (t *ColumnsTx) GroupEventCount(ctx context.Context, view, key string) (uint64, error) {
	col, err := columnViewKey(view)
	if err != nil {
		return 0, err
	}
	st, err := t.statement(ctx, "SELECT count(*) FROM col_event_index WHERE "+col+"=? AND "+col+"<>''")
	if err != nil {
		return 0, err
	}
	var count uint64
	err = st.QueryRowContext(ctx, key).Scan(&count)
	return count, err
}
