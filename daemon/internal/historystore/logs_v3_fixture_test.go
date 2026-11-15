package historystore

// Frozen schema solely for v3 migration fixtures. Production creates column tables.
const logsDDL = `
CREATE TABLE connections(
 flow_key TEXT PRIMARY KEY, started_ms INTEGER NOT NULL DEFAULT 0, last_activity_ms INTEGER NOT NULL,
 app_id TEXT NOT NULL DEFAULT '', domain TEXT NOT NULL DEFAULT '', ip TEXT NOT NULL DEFAULT '',
 issue_code TEXT NOT NULL DEFAULT '', body BLOB NOT NULL);
CREATE INDEX connections_activity ON connections(last_activity_ms);
CREATE INDEX connections_app ON connections(app_id, last_activity_ms);
CREATE INDEX connections_domain ON connections(domain, last_activity_ms);
CREATE INDEX connections_ip ON connections(ip, last_activity_ms);
CREATE INDEX connections_issue ON connections(issue_code, last_activity_ms);
CREATE TABLE events(
 seq INTEGER PRIMARY KEY AUTOINCREMENT, event_id TEXT NOT NULL UNIQUE, at_ms INTEGER NOT NULL,
 kind TEXT NOT NULL DEFAULT '', flow_key TEXT NOT NULL DEFAULT '', level TEXT NOT NULL DEFAULT '',
 source TEXT NOT NULL DEFAULT '', problem_key TEXT NOT NULL DEFAULT '', body BLOB NOT NULL, raw BLOB);
CREATE INDEX events_at ON events(at_ms, event_id);
CREATE INDEX events_flow ON events(flow_key, at_ms);
CREATE INDEX events_problem ON events(problem_key, at_ms);
CREATE TABLE diagnostics(
 flow_key TEXT NOT NULL, seq INTEGER NOT NULL, at_ms INTEGER NOT NULL, text TEXT NOT NULL,
 PRIMARY KEY(flow_key, seq));
CREATE INDEX diagnostics_at ON diagnostics(at_ms);
CREATE TABLE commands(id TEXT PRIMARY KEY, signature TEXT NOT NULL DEFAULT '', at_ms INTEGER NOT NULL, result BLOB);
CREATE TABLE job_cursor(name TEXT PRIMARY KEY, session TEXT NOT NULL, seq INTEGER NOT NULL) WITHOUT ROWID;
INSERT INTO meta(key,value) VALUES
 ('retention_hours','24'),('settings_revision','0'),('logs_revision','0'),
 ('log_deletion_generation','0'),('summary_generation',''),('clear_generation','0'),
 ('log_cleared_at','0'),('journal_generation',''),('partial','0'),('error_code',''),('capacity_evicted','0');
`
