package aiusage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"time"
)

// Enrich only indexed SQLite conversations. The native catalog also includes
// legacy conversations and player content, which do not belong in this index.
func antigravityCatalogTitles(ctx context.Context, root string, entries map[SessionKey]SessionCatalogEntry) string {
	path := filepath.Join(root, "conversation_summaries.db")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil || !info.Mode().IsRegular() {
		return "native_database_unavailable"
	}
	ctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	titles := make(map[SessionKey]SessionCatalogEntry)
	err = withAgyDB(ctx, path, 3, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT conversation_id,substr(title,1,512),
			coalesce(CAST(round(unixepoch(last_modified_time,'subsec')*1000) AS INTEGER),0)
			FROM conversation_summaries LIMIT 10001`)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		count := 0
		for rows.Next() {
			count++
			if count > 10000 {
				return ErrUnavailable
			}
			var id, title string
			var at int64
			if err := rows.Scan(&id, &title, &at); err != nil {
				return err
			}
			key := SessionKey{antigravity, id}
			entry, ok := entries[key]
			if ok {
				entry.LastActivity = max(entry.LastActivity, at)
				entry.setTitle(title, "antigravity.database")
				titles[key] = entry
			}
		}
		return rows.Err()
	})
	if err != nil {
		return "native_database_unavailable"
	}
	for key, entry := range titles {
		entries[key] = entry
	}
	return ""
}
