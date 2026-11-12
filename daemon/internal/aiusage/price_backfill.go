package aiusage

import (
	"context"
	"database/sql"
)

// backfillMissingPrices completes retained rows first collected before their model
// had a tariff. It preserves their identity, attribution and recorded multiplier;
// priced history and models still absent from the table are never repriced.
// Open owns this transaction before collection or read queries can start.
func backfillMissingPrices(ctx context.Context, tx *sql.Tx, retained int64) error {
	stmt, err := tx.PrepareContext(ctx, requestInsert)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = stmt.Close() }()
	cli, key := "", ""
	changed := false
	for {
		rows, err := tx.QueryContext(ctx, "SELECT "+requestColumns+" FROM requests WHERE at>=? AND input_price IS NULL AND output_price IS NULL AND json_extract(details,'$.Prices[0]') IS NULL AND (cli,request_key)>(?,?) ORDER BY cli,request_key LIMIT 500", retained, cli, key)
		if err != nil {
			return ErrUnavailable
		}
		var batch []Request
		for rows.Next() {
			r, err := scanRequest(rows)
			if err != nil {
				_ = rows.Close()
				return ErrUnavailable
			}
			batch = append(batch, r)
		}
		_ = rows.Close()
		if rows.Err() != nil {
			return ErrUnavailable
		}
		grouped := map[bucketKey]Bucket{}
		for _, old := range batch {
			next := old
			next.price()
			if next.InputPrice == nil {
				continue
			}
			if err := writeRequest(ctx, stmt, next); err != nil {
				return err
			}
			accumulate(grouped, old, -1)
			accumulate(grouped, next, 1)
			changed = true
		}
		if err := writeBuckets(ctx, tx, grouped); err != nil {
			return err
		}
		if len(batch) < 500 {
			break
		}
		last := batch[len(batch)-1]
		cli, key = last.CLI, last.Key
	}
	if changed {
		if _, err := tx.ExecContext(ctx, "UPDATE meta SET value=value+1 WHERE key='revision'"); err != nil {
			return ErrUnavailable
		}
	}
	return nil
}
