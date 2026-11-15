package controller

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// The production owner and the isolated converter share these projection rules.
// SQLite I/O stays in historystore; protocol mapping stays in controller.
type columnProjectionSource interface {
	GroupEvents(context.Context, string, string) ([]config.HistoryEvent, error)
	Connection(context.Context, string) (config.LogConnectionSummary, bool, error)
	Event(context.Context, string) (config.HistoryEvent, bool, error)
}

func columnEventIndex(e config.HistoryEvent) historystore.ColumnEventIndex {
	i := historystore.ColumnEventIndex{ID: e.ID, Sequence: e.Sequence, At: e.At, Activity: logViewKey(e, "activity"), Access: logViewKey(e, "access"), Problem: logViewKey(e, "problems"), Operation: logViewKey(e, "operations"), SystemCategory: config.EventCategoryFor(e).Key}
	if f := e.Flow; f != nil {
		i.Route, i.FlowKey = f.RouteID, f.Key
		i.RulePackage, _ = config.TrafficRuleDescription(f.Rule, f.RuleSource)
		i.Latency, i.LatencyKnown = config.FlowLatency(*f)
	}
	return i
}

func columnConnectionMembers(ctx context.Context, source columnProjectionSource, s config.LogConnectionSummary) ([]historystore.ColumnMember, error) {
	var event config.HistoryEvent
	if s.LastEventID != "" {
		e, ok, err := source.Event(ctx, s.LastEventID)
		if err != nil {
			return nil, err
		}
		if ok {
			event = e
		}
	}
	f := retainedConnectionFlow(s, event, time.UnixMilli(1<<62)).Fact
	ids := aggregateIdentities(f)
	ids = append(ids, aggregateIdentities(s.Fact)...)
	seen := map[string]bool{}
	var out []historystore.ColumnMember
	lat, known := config.FlowLatency(f)
	for _, id := range ids {
		key := aggregateMemberKey(id)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, historystore.ColumnMember{Dimension: id.Dimension, Object: id.Object, Name: id.Name, Kind: id.Kind, Route: id.Route, RouteName: id.RouteName, Direction: id.Direction, Search: id.Search, RulePackage: id.RulePackage, StartedAt: s.Fact.StartedAt, LatencyKnown: known, Latency: lat})
	}
	return out, nil
}

func projectColumnGroup(ctx context.Context, source columnProjectionSource, view, key string) (historystore.ColumnGroup, error) {
	stream, ok := source.(columnGroupStream)
	if !ok {
		return historystore.ColumnGroup{}, fmt.Errorf("history columns: stream unavailable")
	}
	return projectColumnGroupStream(ctx, stream, view, key, time.Time{}, "collecting")
}

// indexColumnBatch is the import callback: keys and memberships commit with
// facts/checkpoints. Complete group rows are checkpointed in the later phase.
func indexColumnBatch(ctx context.Context, tx *historystore.ColumnsTx, b historystore.ColumnImportBatch) error {
	flows := map[string]bool{}
	summaries := map[string]config.LogConnectionSummary{}
	evidence := columnBatchEvidence{ColumnsTx: tx, events: map[string]config.HistoryEvent{}}
	for _, e := range b.Events {
		evidence.events[e.ID] = e
		if err := tx.PutEventIndex(ctx, columnEventIndex(e)); err != nil {
			return err
		}
		if e.Flow != nil {
			flows[e.Flow.Key] = true
		}
	}
	for _, s := range b.Connections {
		flows[s.Fact.Key] = true
		summaries[s.Fact.Key] = s
	}
	for _, e := range b.RemovedEvents {
		if e.Flow != nil {
			flows[e.Flow.Key] = true
		}
	}
	for key := range flows {
		s, ok := summaries[key]
		var err error
		if !ok {
			s, ok, err = tx.Connection(ctx, key)
		}
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		m, err := columnConnectionMembers(ctx, evidence, s)
		if err != nil {
			return err
		}
		if err = tx.PutMembers(ctx, key, m); err != nil {
			return err
		}
		traffic, err := columnTrafficEvidence(ctx, evidence, s)
		if err != nil {
			return err
		}
		if err = tx.PutTrafficEvidence(ctx, traffic); err != nil {
			return err
		}
	}
	return nil
}

// projectColumnBatch runs after fact writes, inside the same transaction. This
// is the single batch projection seam for the S2 production owner; failures
// abort facts, indexes, memberships and group rows together.
func projectColumnBatch(ctx context.Context, tx *historystore.ColumnsTx, b historystore.ColumnImportBatch) error {
	if err := indexColumnBatch(ctx, tx, b); err != nil {
		return err
	}
	batchGroups := newColumnBatchGroups(b)
	touched := map[string]map[string]bool{}
	add := func(view, key string) {
		if key == "" {
			return
		}
		if touched[view] == nil {
			touched[view] = map[string]bool{}
		}
		touched[view][key] = true
	}
	for _, e := range b.Events {
		for _, view := range []string{"activity", "access", "problems", "operations"} {
			add(view, logViewKey(e, view))
		}
	}
	for _, s := range b.Connections {
		add("activity", accessRecordID(s.Fact.Key))
	}
	for _, e := range b.RemovedEvents {
		for _, view := range []string{"activity", "access", "problems", "operations"} {
			add(view, logViewKey(e, view))
		}
	}
	for _, key := range b.RemovedConnections {
		add("activity", accessRecordID(key))
	}

	for _, view := range []string{"activity", "access", "problems", "operations"} {
		keys := make([]string, 0, len(touched[view]))
		for key := range touched[view] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {

			g, err := batchGroups.project(ctx, tx, view, key)
			if err != nil {
				return err
			}
			if g.Count == 0 {
				if err = tx.DeleteGroup(ctx, view, key); err != nil {
					return err
				}
				continue
			}
			if err = tx.PutGroup(ctx, g); err != nil {
				return err
			}
		}
	}
	return nil
}

// rebuildColumnProjections is resumable and bounded by phase/key. It does not
// attach observers, publish a read model or start an owner goroutine.
func rebuildColumnProjectionInputs(ctx context.Context, c *historystore.Columns) error {
	if err := c.EnsureProjections(ctx); err != nil {
		return err
	}
	after, err := c.ProjectionProgress(ctx, "events")
	if err != nil {
		return err
	}
	seq := uint64(0)
	if after != "" {
		seq, err = strconv.ParseUint(after, 10, 64)
		if err != nil {
			return err
		}
	}
	for {
		batch, er := c.EventsPage(ctx, seq, 128)
		if er != nil {
			return er
		}
		if len(batch) == 0 {
			break
		}
		seq = batch[len(batch)-1].Sequence
		if er = c.Update(ctx, func(tx *historystore.ColumnsTx) error {
			for _, e := range batch {
				if e2 := tx.PutEventIndex(ctx, columnEventIndex(e)); e2 != nil {
					return e2
				}
			}
			return tx.SetProjectionProgress(ctx, "events", strconv.FormatUint(seq, 10))
		}); er != nil {
			return er
		}
	}
	after, err = c.ProjectionProgress(ctx, "members")
	if err != nil {
		return err
	}
	for {
		batch, er := c.ConnectionsPage(ctx, after, 128)
		if er != nil {
			return er
		}
		if len(batch) == 0 {
			break
		}
		members := make([][]historystore.ColumnMember, len(batch))
		for i, s := range batch {
			members[i], er = columnConnectionMembers(ctx, c, s)
			if er != nil {
				return er
			}
		}
		after = batch[len(batch)-1].Fact.Key
		if er = c.Update(ctx, func(tx *historystore.ColumnsTx) error {
			for i, s := range batch {
				if e2 := tx.PutMembers(ctx, s.Fact.Key, members[i]); e2 != nil {
					return e2
				}
			}
			return tx.SetProjectionProgress(ctx, "members", after)
		}); er != nil {
			return er
		}
	}
	return nil
}

func rebuildColumnProjections(ctx context.Context, c *historystore.Columns) error {
	if err := rebuildColumnProjectionInputs(ctx, c); err != nil {
		return err
	}
	for _, view := range []string{"activity", "access", "problems", "operations"} {
		name := "groups/" + view
		after, err := c.ProjectionProgress(ctx, name)
		if err != nil {
			return err
		}
		for {
			keys, er := c.GroupKeys(ctx, view, after, 64)
			if er != nil {
				return er
			}
			if len(keys) == 0 {
				break
			}
			groups := make([]historystore.ColumnGroup, len(keys))
			for i, key := range keys {
				groups[i], er = projectColumnGroup(ctx, c, view, key)
				if er != nil {
					return er
				}
			}
			after = keys[len(keys)-1]
			if er = c.Update(ctx, func(tx *historystore.ColumnsTx) error {
				for _, g := range groups {
					if e2 := tx.PutGroup(ctx, g); e2 != nil {
						return e2
					}
				}
				return tx.SetProjectionProgress(ctx, name, after)
			}); er != nil {
				return er
			}
		}
	}
	return nil
}
