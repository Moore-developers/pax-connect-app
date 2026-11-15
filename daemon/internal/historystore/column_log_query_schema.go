package historystore

import "context"

// Only unreleased S2 private databases have projection version 2. The bounded
// rewrite copies saved flow values and checkpoints them without recomputing any
// product rule; production v3 JSON upgrades create projection version 4 directly.
func (c *Columns) upgradeGroupOptionColumns(ctx context.Context) error {
	for {
		done := false
		err := c.Update(ctx, func(t *ColumnsTx) error {
			var after string
			if err := t.tx.QueryRowContext(ctx, "SELECT coalesce((SELECT value FROM col_meta WHERE key='option_upgrade_key'),'')").Scan(&after); err != nil {
				return err
			}
			rows, err := t.tx.QueryContext(ctx, "SELECT group_key FROM col_groups WHERE view='activity' AND group_key>? ORDER BY group_key LIMIT 128", after)
			if err != nil {
				return err
			}
			var keys []string
			for rows.Next() {
				var k string
				if err = rows.Scan(&k); err != nil {
					_ = rows.Close()
					return err
				}
				keys = append(keys, k)
			}
			err = rows.Err()
			_ = rows.Close()
			if err != nil {
				return err
			}
			if len(keys) == 0 {
				done = true
				_, err = t.tx.ExecContext(ctx, "UPDATE col_meta SET value='4' WHERE key='projection_version';DELETE FROM col_meta WHERE key='option_upgrade_key';")
				return err
			}
			for _, key := range keys {
				if err = t.exec(ctx, `UPDATE col_groups SET app_name=coalesce((SELECT application FROM col_identities WHERE id=col_groups.identity_id),''),route_name=coalesce((SELECT route_name FROM col_identities WHERE id=col_groups.identity_id),'') WHERE view='activity' AND group_key=?`, key); err != nil {
					return err
				}
			}
			_, err = t.tx.ExecContext(ctx, "INSERT INTO col_meta VALUES('option_upgrade_key',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", keys[len(keys)-1])
			return err
		})
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}
