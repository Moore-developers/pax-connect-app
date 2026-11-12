package aiprofile

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

// Attribution is a credential-free Profile and the price multiplier in effect at one instant.
type Attribution struct {
	ProfileID, ProfileName string
	ServiceSource          string
	// Since bounds configuration observations; zero preserves legacy apply semantics.
	Since      int64
	Multiplier float64
	// Models holds per-model multipliers that override Multiplier; nil means none.
	Models map[string]float64
}

// ForModel is the multiplier a request for model pays under this attribution.
func (a Attribution) ForModel(model string) float64 {
	if v, ok := a.Models[model]; ok {
		return v
	}
	return a.Multiplier
}

func (m *Manager) openEvents() error {
	return m.openEventsAt(filepath.Join(m.dir, "profile_events.db"))
}

func (m *Manager) openEventsAt(path string) error {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		info, e := os.Lstat(path + suffix)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return StoreInvalid
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != m.owner || stat.Nlink != 1 || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
			return StoreInvalid
		}
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return StoreInvalid
	}
	if e = f.Close(); e != nil {
		return StoreInvalid
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"}
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return StoreInvalid
	}
	db.SetMaxOpenConns(1)
	m.events = db
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var version int
	if e = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); e != nil || (version < 0 || version > 3) {
		return StoreInvalid
	}
	if version == 1 { // v1 predates per-model multipliers: add the column in place, once
		var n int
		if e = db.QueryRowContext(ctx, "SELECT count(*) FROM pragma_table_info('profile_events') WHERE name='model_multipliers'").Scan(&n); e != nil {
			return StoreInvalid
		}
		if _, e = db.ExecContext(ctx, `ALTER TABLE profile_events ADD COLUMN model_multipliers TEXT NOT NULL DEFAULT ''`); n == 0 && e != nil {
			return StoreInvalid
		}
	}
	if _, e = db.ExecContext(ctx, `PRAGMA journal_mode=WAL;
 CREATE TABLE IF NOT EXISTS profile_events(event_key TEXT PRIMARY KEY,cli TEXT NOT NULL,at INTEGER NOT NULL,profile_id TEXT NOT NULL,profile_name TEXT NOT NULL,multiplier REAL NOT NULL,kind TEXT NOT NULL,model_multipliers TEXT NOT NULL DEFAULT '',service_source TEXT NOT NULL DEFAULT '',source_since INTEGER NOT NULL DEFAULT 0);
 CREATE INDEX IF NOT EXISTS event_cli_at ON profile_events(cli,at);
 CREATE INDEX IF NOT EXISTS event_profile_at ON profile_events(profile_id,at);
	`); e != nil {
		return StoreInvalid
	}
	if version == 1 || version == 2 {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return StoreInvalid
		}
		defer func() { _ = tx.Rollback() }()
		for _, col := range []struct{ name, definition string }{{"service_source", "TEXT NOT NULL DEFAULT ''"}, {"source_since", "INTEGER NOT NULL DEFAULT 0"}} {
			var n int
			if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM pragma_table_info('profile_events') WHERE name=?", col.name).Scan(&n); err != nil {
				return StoreInvalid
			}
			if n == 0 {
				if _, err = tx.ExecContext(ctx, "ALTER TABLE profile_events ADD COLUMN "+col.name+" "+col.definition); err != nil {
					return StoreInvalid
				}
			}
		}
		if _, err = tx.ExecContext(ctx, "PRAGMA user_version=3"); err != nil {
			return StoreInvalid
		}
		return cause(tx.Commit())
	}
	_, e = db.ExecContext(ctx, "PRAGMA user_version=3")
	return cause(e)
}

func (m *Manager) recordEvent(ctx context.Context, key string, cli CLI, at int64, p Attribution, kind string) error {
	if m.events == nil {
		return StoreInvalid
	}
	models := ""
	if len(p.Models) > 0 {
		b, e := json.Marshal(p.Models) // map keys marshal sorted: the snapshot is deterministic
		if e != nil {
			return StoreInvalid
		}
		models = string(b)
	}
	_, e := m.events.ExecContext(ctx, "INSERT OR IGNORE INTO profile_events(event_key,cli,at,profile_id,profile_name,multiplier,kind,model_multipliers,service_source,source_since) VALUES(?,?,?,?,?,?,?,?,?,?)", key, cli, at, p.ProfileID, p.ProfileName, p.Multiplier, kind, models, p.ServiceSource, p.Since)
	return cause(e)
}
func (m *Manager) repairEvents(ctx context.Context) error {
	for cli, a := range m.recovery.Applied {
		if a.EventAt != 0 {
			since := int64(0)
			if a.EventSource != "" {
				since = a.EventAt / 1e6
			}
			if e := m.recordEvent(ctx, "apply:"+string(cli)+":"+a.OperationID, cli, a.EventAt, Attribution{ProfileID: a.ProfileID, ProfileName: a.EventName, Multiplier: a.EventMultiplier, Models: a.EventModels, ServiceSource: a.EventSource, Since: since}, "apply"); e != nil {
				return e
			}
		}
	}
	for _, p := range m.profiles {
		if p.MetadataAt != 0 {
			if e := m.recordEvent(ctx, "metadata:"+p.ID+":"+strconv.FormatUint(p.Revision, 10), "", p.MetadataAt, Attribution{ProfileID: p.ID, ProfileName: p.Name, Multiplier: p.Multiplier, Models: p.ModelMultipliers}, "metadata"); e != nil {
				return e
			}
		}
	}
	return nil
}
func connectionDigest(cli CLI, files []nativeFile) string {
	_, v, e := transform(cli, files, "", "")
	if e != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(string(cli) + "\x00" + v.url + "\x00" + v.key))
	return hex.EncodeToString(sum[:])
}
