package aiusage

import (
	"context"
	"database/sql"
	"strings"
)

// The caller's startup transaction changes only the grouping key. Legacy rows
// keep their exact counters, prices and empty (unrecorded) source.
func migrateHourlySources(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, "ALTER TABLE usage_hourly RENAME TO usage_hourly_before_sources;"+hourlySchema); err != nil {
		return ErrUnavailable
	}
	columns := "hour,cli,profile_id,model,profile_name," + strings.TrimSuffix(summaryColumns, ",unconfirmed_channel")
	_, err := tx.ExecContext(ctx, "INSERT INTO usage_hourly("+columns+") SELECT "+columns+" FROM usage_hourly_before_sources; DROP TABLE usage_hourly_before_sources;")
	if err != nil {
		return ErrUnavailable
	}
	return nil
}

// Rebuild also accepts databases predating this dimension.
func oldBucketSource(ctx context.Context, db *sql.DB) (string, error) {
	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM pragma_table_info('usage_hourly') WHERE name='service_source'").Scan(&n); err != nil {
		return "", ErrUnavailable
	}
	if n == 0 {
		return "''", nil
	}
	return "service_source", nil
}
