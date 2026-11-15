package historystore

import "context"

// Classification depends on the token's installed inventory, not database
// revision. Distinct standard-value inputs live in SQLite; Go visits at most
// 128 at once and calls the controller's existing classification rule.
func (s *ColumnLogSelection) classifyInputs(ctx context.Context) error {
	var afterApp, afterProcess string
	first := true
	for {
		where := ""
		var args []any
		if !first {
			where = " WHERE (application_id>? OR (application_id=? AND process>?))"
			args = []any{afterApp, afterApp, afterProcess}
		}
		rows, err := s.q.QueryContext(ctx, "SELECT application_id,process FROM (SELECT DISTINCT application_id,process FROM snap_col_identities UNION SELECT application_id,process FROM log_identity_delta)"+where+" ORDER BY application_id,process LIMIT 128", args...)
		if err != nil {
			return err
		}
		type input struct{ app, process string }
		var batch []input
		for rows.Next() {
			var v input
			if err = rows.Scan(&v.app, &v.process); err != nil {
				_ = rows.Close()
				return err
			}
			batch = append(batch, v)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
		for _, v := range batch {
			if app, ok := s.Window.Classify(v.app, v.process); ok {
				if _, err = s.q.ExecContext(ctx, "INSERT INTO log_app_matches VALUES(?,?,?,?)", v.app, v.process, app.ID, app.Name); err != nil {
					return err
				}
			}
		}
		if len(batch) < 128 {
			// The match set is much smaller than the identity table. Tuple IN
			// lets SQLite probe the existing (application_id,process) index
			// instead of scanning every identity for each query.
			_, err = s.q.ExecContext(ctx, `INSERT INTO log_identity_matches SELECT i.id FROM snap_col_identities i WHERE (i.application_id,i.process) IN (SELECT app_id,process FROM log_app_matches) UNION ALL SELECT i.id FROM log_identity_delta i WHERE (i.application_id,i.process) IN (SELECT app_id,process FROM log_app_matches);`)
			return err
		}
		last := batch[len(batch)-1]
		afterApp, afterProcess = last.app, last.process
		first = false
	}
}
