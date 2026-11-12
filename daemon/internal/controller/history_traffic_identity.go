package controller

import (
	"context"
	"encoding/json"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// trafficObservations freezes the latest identity per connection before a transaction counts
// any of its records. Timing, status and latency remain those of the original observation.
func (t *historyTraffic) trafficObservations(ctx context.Context, rows []historystore.Observation) (map[string]config.FlowFact, error) {
	latest := map[string]config.FlowFact{}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if row.Kind == journalGapKind || row.Kind == journalPollKind {
			continue
		}
		var o config.Observation
		if err := json.Unmarshal(row.Body, &o); err != nil {
			return nil, err
		}
		if o.Flow == nil {
			continue
		}
		f := cloneFlowFact(*o.Flow)
		t.apps.identifyContext(ctx, &f)
		if t.route != nil {
			t.route(&f)
		}
		latest[f.Key] = f
	}
	return latest, nil
}

// trafficIdentity overlays only attribution fields. In particular a later failure or response
// must not move its counter to the start time of an earlier record.
func trafficIdentity(f, latest config.FlowFact) config.FlowFact {
	f.ApplicationID, f.Application, f.Process, f.ProcessStatus = latest.ApplicationID, latest.Application, latest.Process, latest.ProcessStatus
	f.Domain, f.IP, f.IPSource = latest.Domain, latest.IP, latest.IPSource
	f.RouteID, f.RouteName, f.Direction = latest.RouteID, latest.RouteName, latest.Direction
	return f
}

// latestUncommitted freezes all identities visible before this transaction. A
// backlog reuses its existing map but scans arrivals after the previous horizon.
func (t *historyTraffic) latestUncommitted(ctx context.Context, first []historystore.Observation) (map[string]config.FlowFact, error) {
	tail, err := t.journal.Tail(ctx)
	if err != nil {
		t.consumers.reportDamage(err)
		return nil, err
	}
	if t.identities != nil && tail == t.identityThrough {
		return t.identities, nil
	}
	latest := map[string]config.FlowFact{}
	cursor := t.state.cursor
	rows := first
	if t.identities != nil {
		latest = t.identities
		cursor = t.identityThrough
		rows, err = t.journal.ReadAfter(ctx, cursor, trafficBatchRecords)
		if err != nil {
			t.consumers.reportDamage(err)
			return nil, err
		}
	}
	for {
		reached := false
		for i, row := range rows {
			if row.Session == tail.Session && row.Seq == tail.Seq {
				rows = rows[:i+1]
				reached = true
				break
			}
		}
		part, e := t.trafficObservations(ctx, rows)
		if e != nil {
			return nil, e
		}
		for k, f := range part {
			latest[k] = f
		}
		if len(rows) == 0 || reached {
			t.identityThrough = cursor
			if len(rows) > 0 {
				last := rows[len(rows)-1]
				t.identityThrough = historystore.Cursor{Session: last.Session, Seq: last.Seq}
			}
			return latest, nil
		}
		last := rows[len(rows)-1]
		cursor = historystore.Cursor{Session: last.Session, Seq: last.Seq}
		rows, e = t.journal.ReadAfter(ctx, cursor, trafficBatchRecords)
		if e != nil {
			t.consumers.reportDamage(e)
			return nil, e
		}
	}
}
