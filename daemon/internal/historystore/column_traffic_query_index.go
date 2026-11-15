package historystore

import (
	"context"
	"time"

	"pax-connect/daemon/internal/config"
)

// ColumnTrafficEvidence is the narrow, production-derived lookup material.
// Complete flow evidence remains in column facts and is fetched only by page.
type ColumnTrafficEvidence struct {
	Fact                                                     config.FlowFact
	SourceKind, SourceID, SourceName, GroupID, FutureGroupID string
	CorrectionAt                                             time.Time
	Corrected                                                config.FlowFact
	Latency                                                  uint64
	LatencyKnown                                             bool
	Download                                                 uint64
}

const columnTrafficEvidenceDDL = `CREATE TABLE IF NOT EXISTS col_traffic(
 flow_key TEXT PRIMARY KEY,start_ns INTEGER NOT NULL,closed_ns INTEGER,observed_ns INTEGER,
 direction TEXT NOT NULL,route_id TEXT NOT NULL,route_name TEXT NOT NULL,rule TEXT NOT NULL,rule_source TEXT NOT NULL,
 correction_ns INTEGER NOT NULL,corrected_direction TEXT NOT NULL,corrected_route TEXT NOT NULL,corrected_name TEXT NOT NULL,corrected_rule TEXT NOT NULL,corrected_source TEXT NOT NULL,
 source_kind TEXT NOT NULL,source_id TEXT NOT NULL,source_name TEXT NOT NULL,
 application_id TEXT NOT NULL,application TEXT NOT NULL,process TEXT NOT NULL,domain TEXT NOT NULL,ip TEXT NOT NULL,
 dns_type TEXT NOT NULL,dns_result TEXT NOT NULL,dns_group TEXT NOT NULL,future_dns_group TEXT NOT NULL,
 latency_known INTEGER NOT NULL,latency INTEGER NOT NULL,establishment_state TEXT NOT NULL,establishment_ms INTEGER NOT NULL,failure TEXT NOT NULL,download INTEGER NOT NULL
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS col_traffic_dns_v2 ON col_traffic(start_ns DESC,flow_key) WHERE source_kind='dns';
CREATE INDEX IF NOT EXISTS col_traffic_dns_period_v2 ON col_traffic(start_ns,closed_ns,observed_ns,latency_known,latency,establishment_state,establishment_ms,failure,flow_key) WHERE source_kind='dns';
CREATE INDEX IF NOT EXISTS col_traffic_dns_basis_v2 ON col_traffic(rule,rule_source,start_ns DESC,flow_key,route_name,closed_ns,observed_ns) WHERE source_kind='dns';
INSERT OR IGNORE INTO col_meta(key,value) SELECT 'traffic_ready',CASE WHEN EXISTS(SELECT 1 FROM col_connections) THEN '0' ELSE '1' END;
INSERT OR IGNORE INTO col_meta VALUES('traffic_progress','');`

// Only unreleased 209 private fixtures have the superseded full-population
// DNS indexes/unused labels. Production first opens this schema directly.
func (t *ColumnsTx) upgradeColumnTrafficEvidence(ctx context.Context) error {
	for _, name := range []string{"col_traffic_dns", "col_traffic_dns_period", "col_traffic_dns_basis"} {
		if _, err := t.tx.ExecContext(ctx, "DROP INDEX IF EXISTS "+name); err != nil {
			return err
		}
	}
	for _, name := range []string{"source_label", "source_evidence"} {
		var exists bool
		if err := t.tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pragma_table_info('col_traffic') WHERE name=?)", name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			if _, err := t.tx.ExecContext(ctx, "ALTER TABLE col_traffic DROP COLUMN "+name); err != nil {
				return err
			}
		}
	}
	var version string
	err := t.tx.QueryRowContext(ctx, "SELECT coalesce((SELECT value FROM col_meta WHERE key='traffic_version'),'')").Scan(&version)
	if err != nil {
		return err
	}
	if version == "2" {
		return nil
	}
	if _, err = t.tx.ExecContext(ctx, "UPDATE col_traffic SET dns_group='',future_dns_group='' WHERE source_kind<>'dns'"); err != nil {
		return err
	}
	return t.exec(ctx, "INSERT INTO col_meta VALUES('traffic_version','2') ON CONFLICT(key) DO UPDATE SET value=excluded.value")
}

// PutTrafficEvidence commits the same owner's narrow query inputs with facts.
func (t *ColumnsTx) PutTrafficEvidence(ctx context.Context, e ColumnTrafficEvidence) error {
	f := e.Fact
	dnsType, dnsResult := "", ""
	if f.DNS != nil {
		dnsType, dnsResult = f.DNS.QueryType, f.DNS.Result
	}
	var closed, observed any
	if f.ClosedAt != nil {
		closed = f.ClosedAt.UnixNano()
	}
	if f.ObservedUntil != nil {
		observed = f.ObservedUntil.UnixNano()
	}
	correction := int64(0)
	if !e.CorrectionAt.IsZero() {
		correction = e.CorrectionAt.UnixNano()
	}
	values := []any{f.Key, f.StartedAt.UnixNano(), closed, observed, f.Direction, f.RouteID, f.RouteName, f.Rule, f.RuleSource, correction, e.Corrected.Direction, e.Corrected.RouteID, e.Corrected.RouteName, e.Corrected.Rule, e.Corrected.RuleSource, e.SourceKind, e.SourceID, e.SourceName, f.ApplicationID, f.Application, f.Process, f.Domain, f.IP, dnsType, dnsResult, e.GroupID, e.FutureGroupID, e.LatencyKnown, int64(e.Latency), f.EstablishmentState, int64(f.EstablishmentMS), f.Failure, int64(e.Download)}
	const names = "flow_key,start_ns,closed_ns,observed_ns,direction,route_id,route_name,rule,rule_source,correction_ns,corrected_direction,corrected_route,corrected_name,corrected_rule,corrected_source,source_kind,source_id,source_name,application_id,application,process,domain,ip,dns_type,dns_result,dns_group,future_dns_group,latency_known,latency,establishment_state,establishment_ms,failure,download"
	return t.exec(ctx, columnInsert("col_traffic", names, "ON CONFLICT(flow_key) DO UPDATE SET "+columnUpdates(names)), values...)
}

// TrafficProjectionReady reports the one-time narrow-index backfill state.
func (c *Columns) TrafficProjectionReady(ctx context.Context) (bool, error) {
	var value string
	err := c.r.QueryRowContext(ctx, "SELECT value FROM col_meta WHERE key='traffic_ready'").Scan(&value)
	return value == "1", err
}

// TrafficProjectionProgress is the last committed summary key in its backfill.
func (c *Columns) TrafficProjectionProgress(ctx context.Context) (string, error) {
	var value string
	err := c.r.QueryRowContext(ctx, "SELECT value FROM col_meta WHERE key='traffic_progress'").Scan(&value)
	return value, err
}

// SetTrafficProjectionProgress commits the checkpoint and readiness with rows.
func (t *ColumnsTx) SetTrafficProjectionProgress(ctx context.Context, key string, done bool) error {
	if err := t.exec(ctx, "UPDATE col_meta SET value=? WHERE key='traffic_progress'", key); err != nil {
		return err
	}
	if done {
		return t.exec(ctx, "UPDATE col_meta SET value='1' WHERE key='traffic_ready'")
	}
	return nil
}
