package aiusage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"strings"
)

const sessionCatalogSchema = `
CREATE TABLE IF NOT EXISTS session_catalog(cli TEXT NOT NULL,session TEXT NOT NULL,title TEXT NOT NULL,title_source TEXT NOT NULL,title_status TEXT NOT NULL,last_activity INTEGER NOT NULL,is_child INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(cli,session));
CREATE INDEX IF NOT EXISTS session_catalog_activity ON session_catalog(last_activity DESC,cli,session);
CREATE TABLE IF NOT EXISTS session_catalog_files(path TEXT PRIMARY KEY,cli TEXT NOT NULL,inode INTEGER NOT NULL,size INTEGER NOT NULL,mtime INTEGER NOT NULL,offset INTEGER NOT NULL,state BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS session_catalog_status(cli TEXT PRIMARY KEY,error TEXT NOT NULL);
INSERT OR IGNORE INTO meta VALUES('catalog_revision',0);
INSERT OR IGNORE INTO meta VALUES('catalog_ready',0);
`

// SessionCatalogEntry is native display metadata, never an accounting contribution.
type SessionCatalogEntry struct {
	Key                        SessionKey
	Title, TitleSource, Status string
	LastActivity               int64
	Child                      bool
	Unlinked                   bool
}

// SessionCatalogQuery selects a directory page by activity, request presence and text.
type SessionCatalogQuery struct {
	CLI, Text, PageToken string
	From, To             int64
	PageSize             int
	Selected             *SessionKey
}

// SessionCatalogPage carries both revisions and CLI counts within the date range.
type SessionCatalogPage struct {
	Items                []SessionCatalogEntry
	Selected             *SessionCatalogEntry
	Counts               map[string]int64
	Sources              map[string]string
	Revision             uint64
	UsageRevision        uint64
	Ready, ResetRequired bool
	Total                int64
	NextPageToken        string
}

type catalogPageToken struct {
	Revision      uint64
	UsageRevision uint64
	Offset        int
	CLI, Text     string
	From, To      int64
}

func validSessionKey(k SessionKey) bool {
	return (k.CLI == "claude" || k.CLI == "codex" || k.CLI == "dsh") && k.Session != "" && len(k.Session) <= 512
}

// ListSessions reads metadata and retained request presence in one SQLite snapshot.
func (s *Store) ListSessions(ctx context.Context, q SessionCatalogQuery) (SessionCatalogPage, error) {
	out := SessionCatalogPage{Counts: map[string]int64{}, Sources: map[string]string{}}
	q.Text = strings.TrimSpace(q.Text)
	if (q.From != 0 || q.To != 0) && (q.From <= 0 || q.To <= q.From) {
		return out, ErrRange
	}
	if (q.CLI != "" && !validSessionKey(SessionKey{q.CLI, "id"})) || len(q.Text) > 1024 || len(q.PageToken) > 4096 || q.PageSize < 0 || q.PageSize > 200 || (q.Selected != nil && !validSessionKey(*q.Selected)) {
		return out, ErrRange
	}
	if q.PageSize == 0 {
		q.PageSize = 100
	}
	if s.RebuildRequired() {
		return out, ErrUnavailable
	}
	tx, err := s.r.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, ErrUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	if err = tx.QueryRowContext(ctx, `SELECT (SELECT value FROM meta WHERE key='catalog_revision'),(SELECT value FROM meta WHERE key='catalog_ready'),(SELECT value FROM meta WHERE key='revision')`).Scan(&out.Revision, &out.Ready, &out.UsageRevision); err != nil {
		return out, ErrUnavailable
	}
	var token catalogPageToken
	if q.PageToken != "" {
		raw, e := base64.RawURLEncoding.DecodeString(q.PageToken)
		if e != nil || json.Unmarshal(raw, &token) != nil || token.Offset < 0 || token.CLI != q.CLI || token.Text != q.Text || token.From != q.From || token.To != q.To {
			return out, ErrRange
		}
		if token.Revision != out.Revision || token.UsageRevision != out.UsageRevision {
			out.ResetRequired = true
			return out, nil
		}
	}
	items, counts, selected, sources, err := catalogCandidates(ctx, tx, q)
	if err != nil {
		return out, ErrUnavailable
	}
	out.Counts, out.Selected, out.Sources = counts, selected, sources
	out.Total = int64(len(items))
	start := min(token.Offset, len(items))
	end := min(start+q.PageSize, len(items))
	out.Items = items[start:end]
	if end < len(items) {
		raw, _ := json.Marshal(catalogPageToken{Revision: out.Revision, UsageRevision: out.UsageRevision, Offset: end, CLI: q.CLI, Text: q.Text, From: q.From, To: q.To})
		out.NextPageToken = base64.RawURLEncoding.EncodeToString(raw)
	}

	if err = tx.Commit(); err != nil {
		return out, ErrUnavailable
	}
	return out, nil
}
