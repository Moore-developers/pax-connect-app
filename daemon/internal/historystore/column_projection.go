package historystore

import (
	"context"
	"fmt"
	"time"

	"pax-connect/daemon/internal/config"
)

// ColumnEventIndex holds production-derived keys. Filter facts remain in the
// event's identity/state columns, including ordered DNS answers and process.
type ColumnEventIndex struct {
	ID, Activity, Access, Problem, Operation, Route, FlowKey string
	RulePackage, SystemCategory                              string
	Sequence                                                 uint64
	At                                                       time.Time
	LatencyKnown                                             bool
	Latency                                                  uint64
}

// ColumnProjectedFlow retains the exact returned flow, including classification
// outputs, so reading a row does not classify already-classified input again.
type ColumnProjectedFlow struct {
	Fact                                                   config.FlowFact
	PrimaryDomain, SourceKind, SourceLabel, SourceEvidence string
	Upload, Download                                       uint64
}

// ColumnGroup is the scalar production projection for one complete group.
// EventID references immutable system evidence and event-time metadata.
type ColumnGroup struct {
	RulePackage                                                                          string
	LatencyKnown                                                                         bool
	Latency                                                                              uint64
	View, Key, EventID                                                                   string
	FirstAt, LastAt, Until                                                               time.Time
	Count, Related                                                                       uint64
	Level                                                                                int32
	Title, ProblemID, Category, Kind, CategoryName, CategorySymbol, SystemCategory       string
	ApplicationID, Application, Domain, IP, RouteID, RouteName, Direction, ConnectionKey string
	ConnectionID, Session, ConnectionRoute, Target, Stage, Code                          string
	ConnectionDuration                                                                   uint64
	HasConnection                                                                        bool
	Flow                                                                                 *ColumnProjectedFlow
	Metrics                                                                              *config.ConnectionMetrics
}

// ColumnMember is one production connection/object membership. Long object
// names live once in col_objects and member indexes use integer references.
type ColumnMember struct {
	Dimension, Object, Name, Kind, Route, RouteName, Direction, Search, RulePackage string
	StartedAt                                                                       time.Time
	LatencyKnown                                                                    bool
	Latency                                                                         uint64
}

const columnProjectionDDL = `
CREATE TABLE IF NOT EXISTS col_event_index(
 event_id TEXT PRIMARY KEY,seq INTEGER NOT NULL,at_ns INTEGER NOT NULL,
 activity TEXT NOT NULL,access TEXT NOT NULL,problem TEXT NOT NULL,operation TEXT NOT NULL,
 route TEXT NOT NULL,flow_key TEXT NOT NULL,rule_package TEXT NOT NULL,system_category TEXT NOT NULL,
 latency_known INTEGER NOT NULL,latency INTEGER NOT NULL) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS col_e_activity ON col_event_index(activity,at_ns,event_id);
CREATE INDEX IF NOT EXISTS col_e_access ON col_event_index(access,at_ns,event_id) WHERE access<>'';
CREATE INDEX IF NOT EXISTS col_e_problem ON col_event_index(problem,at_ns,event_id) WHERE problem<>'';
CREATE INDEX IF NOT EXISTS col_e_operation ON col_event_index(operation,at_ns,event_id) WHERE operation<>'';
CREATE INDEX IF NOT EXISTS col_e_flow ON col_event_index(flow_key,at_ns,event_id) WHERE flow_key<>'';
CREATE INDEX IF NOT EXISTS col_identity_classification ON col_identities(application_id,process);
CREATE TABLE IF NOT EXISTS col_objects(
 id INTEGER PRIMARY KEY,dimension TEXT NOT NULL,object_key TEXT NOT NULL,name TEXT NOT NULL,kind TEXT NOT NULL,
 UNIQUE(dimension,object_key,name,kind));
CREATE TABLE IF NOT EXISTS col_members(
 flow_key TEXT NOT NULL,object_id INTEGER NOT NULL,route TEXT NOT NULL,route_name TEXT NOT NULL,
 direction TEXT NOT NULL,search TEXT NOT NULL,rule_package TEXT NOT NULL,started_at TEXT NOT NULL,started_ns INTEGER NOT NULL,
 latency_known INTEGER NOT NULL,latency INTEGER NOT NULL,
 PRIMARY KEY(flow_key,object_id,route,direction)) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS col_members_object ON col_members(object_id,direction,started_ns DESC,flow_key);
CREATE TABLE IF NOT EXISTS col_projection_progress(name TEXT PRIMARY KEY,value TEXT NOT NULL) WITHOUT ROWID;
INSERT OR IGNORE INTO col_meta VALUES('projection_version','5');`

const groupColumnNames = "view,group_key,event_id,first_at,last_at,until_at,last_ns,event_count,related,level,title,problem_id,category,record_kind,category_name,category_symbol,system_category,app_id,app_name,domain,ip,route_id,route_name,direction,connection_key,connection_id,session,connection_route,target,stage,code,connection_duration,has_connection,flow_present,metrics_present,identity_id,primary_domain,source_kind,source_label,source_evidence,flow_upload,flow_download,rule_package,latency_known,latency,first_ns,until_ns"

const columnGroupsDDL = `CREATE TABLE IF NOT EXISTS col_groups(
 view TEXT NOT NULL,group_key TEXT NOT NULL,event_id TEXT NOT NULL,first_at TEXT NOT NULL,last_at TEXT NOT NULL,until_at TEXT NOT NULL,
 last_ns INTEGER NOT NULL,event_count INTEGER NOT NULL,related INTEGER NOT NULL,level INTEGER NOT NULL,title TEXT NOT NULL,
 problem_id TEXT NOT NULL,category TEXT NOT NULL,record_kind TEXT NOT NULL,category_name TEXT NOT NULL,category_symbol TEXT NOT NULL,
 system_category TEXT NOT NULL,app_id TEXT NOT NULL,app_name TEXT NOT NULL,domain TEXT NOT NULL,ip TEXT NOT NULL,
 route_id TEXT NOT NULL,route_name TEXT NOT NULL,direction TEXT NOT NULL,connection_key TEXT NOT NULL,
 connection_id TEXT NOT NULL,session TEXT NOT NULL,connection_route TEXT NOT NULL,target TEXT NOT NULL,stage TEXT NOT NULL,code TEXT NOT NULL,
 connection_duration INTEGER NOT NULL,has_connection INTEGER NOT NULL,flow_present INTEGER NOT NULL,metrics_present INTEGER NOT NULL,
 identity_id INTEGER NOT NULL,primary_domain TEXT NOT NULL,source_kind TEXT NOT NULL,source_label TEXT NOT NULL,source_evidence TEXT NOT NULL,
 flow_upload INTEGER NOT NULL,flow_download INTEGER NOT NULL,
 rule_package TEXT NOT NULL,latency_known INTEGER NOT NULL,latency INTEGER NOT NULL,
 first_ns INTEGER NOT NULL,until_ns INTEGER NOT NULL,
 routing_matched_at TEXT,phase TEXT,started_at TEXT,closed_at TEXT,observed_until TEXT,response_known INTEGER,response_ms INTEGER,
 state_unknown INTEGER,establishment_state TEXT,establishment_ms INTEGER,failure TEXT,evidence TEXT,
 dns_present INTEGER,dns_type TEXT,dns_result TEXT,dns_answers_present INTEGER,
 duration_ms INTEGER,duration_state TEXT,upload INTEGER,download INTEGER,bytes_state TEXT,bytes_from TEXT,as_of TEXT,metrics_reason TEXT,
 PRIMARY KEY(view,group_key)) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS col_groups_time ON col_groups(view,last_ns DESC,group_key DESC);
CREATE INDEX IF NOT EXISTS col_groups_identity ON col_groups(identity_id);
CREATE INDEX IF NOT EXISTS col_groups_window ON col_groups(view,last_ns DESC,group_key DESC,first_ns,until_ns,event_count,flow_present,identity_id,level,source_kind);
CREATE INDEX IF NOT EXISTS col_groups_domain_window ON col_groups(view,last_ns DESC,group_key DESC,first_ns,until_ns,event_count,flow_present,identity_id,level,source_kind,primary_domain) WHERE view='activity';`

// EnsureProjections creates or versions the derived query tables. Fact fields
// are independent; rebuilding derived rows is scheduled by the log owner.
func (c *Columns) EnsureProjections(ctx context.Context) error {
	migrate := false
	integerMigrate := false
	err := c.Update(ctx, func(t *ColumnsTx) error {
		if _, err := t.tx.ExecContext(ctx, columnProjectionDDL+columnTrafficEvidenceDDL+columnLogOptionsDDL); err != nil {
			return err
		}
		if err := t.upgradeColumnTrafficEvidence(ctx); err != nil {
			return err
		}
		var version string
		if err := t.tx.QueryRowContext(ctx, "SELECT value FROM col_meta WHERE key='projection_version'").Scan(&version); err != nil {
			return err
		}
		if version == "1" {
			// Only unreleased S1 private databases have this projection version.
			// Facts are unchanged; invalidate their rebuild checkpoints atomically.
			if _, err := t.tx.ExecContext(ctx, `DROP TABLE IF EXISTS col_groups;
DELETE FROM col_answers WHERE owner_kind=2;
DELETE FROM col_projection_progress WHERE name LIKE 'groups/%';
UPDATE col_meta SET value='4' WHERE key='projection_version';`); err != nil {
				return err
			}
			version = "4"
		}
		if version == "2" {
			migrate = true
			if _, err := t.tx.ExecContext(ctx, "DROP INDEX IF EXISTS col_groups_window"); err != nil {
				return err
			}
			version = "4"
		}

		if version == "3" {
			if _, err := t.tx.ExecContext(ctx, "DROP INDEX IF EXISTS col_groups_window;UPDATE col_meta SET value='4' WHERE key='projection_version';"); err != nil {
				return err
			}
			version = "4"
		}
		if version == "4" {
			integerMigrate = true
			if _, err := t.tx.ExecContext(ctx, "DROP INDEX IF EXISTS col_groups_window"); err != nil {
				return err
			}
			version = "5"
		}
		if version != "5" {
			return fmt.Errorf("%w: column projection %s", ErrSchemaVersion, version)
		}
		if _, err := t.tx.ExecContext(ctx, columnGroupsDDL); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	if migrate {
		if err = c.upgradeGroupOptionColumns(ctx); err != nil {
			return err
		}
		integerMigrate = true
	}
	if integerMigrate {
		return c.upgradeIntegerOptions(ctx)
	}
	return nil
}

// PutEventIndex writes production-derived query keys in the fact transaction.
func (t *ColumnsTx) PutEventIndex(ctx context.Context, e ColumnEventIndex) error {
	return t.exec(ctx, `INSERT INTO col_event_index VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(event_id) DO UPDATE SET
 seq=excluded.seq,at_ns=excluded.at_ns,activity=excluded.activity,access=excluded.access,problem=excluded.problem,
 operation=excluded.operation,route=excluded.route,flow_key=excluded.flow_key,rule_package=excluded.rule_package,
 system_category=excluded.system_category,latency_known=excluded.latency_known,latency=excluded.latency`,
		e.ID, int64(e.Sequence), e.At.UnixNano(), e.Activity, e.Access, e.Problem, e.Operation, e.Route, e.FlowKey, e.RulePackage, e.SystemCategory, e.LatencyKnown, int64(e.Latency))
}

var columnGroupInsert = columnInsert("col_groups", groupColumnNames+","+stateColumnNames+","+metricsColumnNames, "ON CONFLICT(view,group_key) DO UPDATE SET "+columnUpdates(groupColumnNames+","+stateColumnNames+","+metricsColumnNames))

// PutGroup writes an exact production projection with scalar flow/metric columns.
func (t *ColumnsTx) PutGroup(ctx context.Context, g ColumnGroup) error {
	f := ColumnProjectedFlow{}
	if g.Flow != nil {
		f = *g.Flow
	}
	id, err := t.putIdentity(ctx, f.Fact)
	if err != nil {
		return err
	}
	v := columnGroupValues(g, id)
	if err = t.exec(ctx, columnGroupInsert, v...); err != nil {
		return err
	}
	if err = t.putGroupOptions(ctx, g); err != nil {
		return err
	}
	return t.writeAnswers(ctx, 2, g.View+"\x00"+g.Key, f.Fact)
}

func columnGroupValues(g ColumnGroup, id int64) []any {
	f := ColumnProjectedFlow{}
	if g.Flow != nil {
		f = *g.Flow
	}
	m := config.ConnectionMetrics{}
	if g.Metrics != nil {
		m = *g.Metrics
	}
	v := make([]any, 0, 71)
	v = append(v, g.View, g.Key, g.EventID, columnTime(g.FirstAt), columnTime(g.LastAt), columnTime(g.Until), g.LastAt.UnixNano(), int64(g.Count), int64(g.Related), g.Level, g.Title, g.ProblemID, g.Category, g.Kind, g.CategoryName, g.CategorySymbol, g.SystemCategory, g.ApplicationID, g.Application, g.Domain, g.IP, g.RouteID, g.RouteName, g.Direction, g.ConnectionKey, g.ConnectionID, g.Session, g.ConnectionRoute, g.Target, g.Stage, g.Code, int64(g.ConnectionDuration), g.HasConnection, g.Flow != nil, g.Metrics != nil, id, f.PrimaryDomain, f.SourceKind, f.SourceLabel, f.SourceEvidence, int64(f.Upload), int64(f.Download))
	v = append(v, g.RulePackage, g.LatencyKnown, int64(g.Latency))
	v = append(v, g.FirstAt.UnixNano(), g.Until.UnixNano())
	v = append(v, stateValues(f.Fact)...)
	v = append(v, metricsValues(m)...)
	return v
}

// PutMembers replaces one connection's directory in the same fact transaction.
func (t *ColumnsTx) PutMembers(ctx context.Context, key string, members []ColumnMember) error {
	if err := t.exec(ctx, "DELETE FROM col_members WHERE flow_key=?", key); err != nil {
		return err
	}
	for _, m := range members {
		id, err := t.columnObject(ctx, m)
		if err != nil {
			return err
		}
		if err = t.exec(ctx, "INSERT OR IGNORE INTO col_members VALUES(?,?,?,?,?,?,?,?,?,?,?)", key, id, m.Route, m.RouteName, m.Direction, m.Search, m.RulePackage, columnTime(m.StartedAt), m.StartedAt.UnixNano(), m.LatencyKnown, int64(m.Latency)); err != nil {
			return err
		}
	}
	return nil
}

type columnObjectKey struct{ dimension, object, name, kind string }

func (t *ColumnsTx) columnObject(ctx context.Context, m ColumnMember) (int64, error) {
	key := columnObjectKey{m.Dimension, m.Object, m.Name, m.Kind}
	if id, ok := t.objects[key]; ok {
		return id, nil
	}
	st, err := t.statement(ctx, `INSERT INTO col_objects(dimension,object_key,name,kind) VALUES(?,?,?,?)
 ON CONFLICT(dimension,object_key,name,kind) DO UPDATE SET name=excluded.name RETURNING id`)
	if err != nil {
		return 0, err
	}
	var id int64
	if err = st.QueryRowContext(ctx, m.Dimension, m.Object, m.Name, m.Kind).Scan(&id); err != nil {
		return 0, err
	}
	if len(t.objects) < columnBatchDictionaryLimit {
		if t.objects == nil {
			t.objects = map[columnObjectKey]int64{}
		}
		t.objects[key] = id
	}
	return id, nil
}
