package historystore

// Column facts are shared by production v4 and the isolated converter. The
// v3 DDL lives only in conversion test fixtures.
const columnsDDL = `
CREATE TABLE IF NOT EXISTS col_meta(key TEXT PRIMARY KEY, value TEXT NOT NULL) WITHOUT ROWID;
INSERT OR IGNORE INTO col_meta VALUES('fact_version','2');
CREATE TABLE IF NOT EXISTS col_identities(
 id INTEGER PRIMARY KEY, digest BLOB NOT NULL UNIQUE,
 flow_key TEXT,
 application_id TEXT,
 application TEXT,
 process TEXT,
 domain TEXT,
 ip TEXT,
 port INTEGER,
 transport TEXT,
 route_id TEXT,
 route_name TEXT,
 direction TEXT,
 rule TEXT,
 rule_source TEXT,
 purpose TEXT,
 ip_source TEXT,
 process_status TEXT);
CREATE TABLE IF NOT EXISTS col_events(
 seq INTEGER,
 event_id TEXT,
 at TEXT,
 at_ns INTEGER,
 kind TEXT,
 title TEXT,
 level TEXT,
 source TEXT,
 result TEXT,
 operation_id TEXT,
 operation_from TEXT,
 operation_to TEXT,
 truncated INTEGER,
 raw_omitted TEXT,
 raw_present INTEGER,
 flow_present INTEGER,
 metrics_present INTEGER,
 system_present INTEGER,
 identity_id INTEGER,
 routing_matched_at TEXT,
 phase TEXT,
 started_at TEXT,
 closed_at TEXT,
 observed_until TEXT,
 response_known INTEGER,
 response_ms INTEGER,
 state_unknown INTEGER,
 establishment_state TEXT,
 establishment_ms INTEGER,
 failure TEXT,
 evidence TEXT,
 dns_present INTEGER,
 dns_type TEXT,
 dns_result TEXT,
 dns_answers_present INTEGER,
 duration_ms INTEGER,
 duration_state TEXT,
 upload INTEGER,
 download INTEGER,
 bytes_state TEXT,
 bytes_from TEXT,
 as_of TEXT,
 metrics_reason TEXT,
 system_type TEXT,
 system_name TEXT,
 system_result TEXT,
 system_error TEXT,
 category_key TEXT,
 category_name TEXT,
 category_symbol TEXT,
 system_started_at TEXT,
 sections_present INTEGER,
 PRIMARY KEY(event_id), UNIQUE(seq));
CREATE INDEX IF NOT EXISTS col_events_at ON col_events(at_ns,event_id);
CREATE INDEX IF NOT EXISTS col_events_identity ON col_events(identity_id,at_ns);
CREATE TABLE IF NOT EXISTS col_connections(
 flow_key TEXT,
 started_ns INTEGER NOT NULL DEFAULT 0,
 last_activity_ms INTEGER NOT NULL DEFAULT 0,
 last_event_id TEXT,
 last_log_at TEXT,
 last_sequence INTEGER,
 identity_id INTEGER,
 routing_matched_at TEXT,
 phase TEXT,
 started_at TEXT,
 closed_at TEXT,
 observed_until TEXT,
 response_known INTEGER,
 response_ms INTEGER,
 state_unknown INTEGER,
 establishment_state TEXT,
 establishment_ms INTEGER,
 failure TEXT,
 evidence TEXT,
 dns_present INTEGER,
 dns_type TEXT,
 dns_result TEXT,
 dns_answers_present INTEGER,
 duration_ms INTEGER,
 duration_state TEXT,
 upload INTEGER,
 download INTEGER,
 bytes_state TEXT,
 bytes_from TEXT,
 as_of TEXT,
 metrics_reason TEXT,
 PRIMARY KEY(flow_key)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS col_answers(
 owner_kind INTEGER NOT NULL, owner_key TEXT NOT NULL, ordinal INTEGER NOT NULL, value TEXT NOT NULL,
 PRIMARY KEY(owner_kind,owner_key,ordinal)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS col_texts(event_id TEXT PRIMARY KEY, body BLOB NOT NULL) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS col_raw(
 event_id TEXT NOT NULL, ordinal INTEGER NOT NULL, value TEXT NOT NULL,
 PRIMARY KEY(event_id,ordinal)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS col_sections(
 event_id TEXT NOT NULL, ordinal INTEGER NOT NULL, kind TEXT NOT NULL,title TEXT NOT NULL,fields_present INTEGER NOT NULL,
 PRIMARY KEY(event_id,ordinal)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS col_fields(
 event_id TEXT NOT NULL,section_ordinal INTEGER NOT NULL,ordinal INTEGER NOT NULL,
 label TEXT NOT NULL,value TEXT NOT NULL,before_value TEXT NOT NULL,after_value TEXT NOT NULL,
 PRIMARY KEY(event_id,section_ordinal,ordinal)) WITHOUT ROWID;
`
