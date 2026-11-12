package controller

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
	"pax-connect/daemon/internal/stats"
)

// A version owns directories, while unchanged immutable facts are shared across versions.
// The database owner publishes after commit, including byte-only summary updates.
type logModelConnection struct {
	summary config.LogConnectionSummary
	bytes   int
}
type logModelEvent struct {
	event config.HistoryEvent
	bytes int
}
type logModelVersion struct {
	readBarrier   func(context.Context, func()) error
	serial        uint64
	valid         *atomic.Bool
	removedBefore *atomic.Int64
	meta          historystore.LogsMeta
	connections   map[string]*logModelConnection
	events        map[string]*logModelEvent
	ordered       []*logModelConnection
	members       map[string][]*logModelConnection
	candidates    map[string][]*logModelEvent
	activity      map[string]*logActivityGroup
	bytes         int
}
type historyLogModel struct {
	mu          sync.Mutex
	version     *logModelVersion
	failure     error
	serial      uint64
	owner       *historyLogs
	readBarrier func(context.Context, func()) error
}

// The shared model is separate from the existing 32 MiB token budget. Its conservative
// charge includes decoded values and directories, and every version retained by a live token.
const logModelBudget = 256 << 20

func (l *historyLogs) installReadModel(ctx context.Context) error {
	source, ok := l.logs.(interface {
		ObserveReads(func(context.Context, historystore.LogsChanges))
		ReadSnapshot(context.Context) (historystore.LogsChanges, error)
	})
	if !ok {
		return nil
	} // test doubles continue to exercise the SQLite-compatible read seam
	m := &historyLogModel{owner: l}
	if barrier, ok := l.logs.(interface {
		ReadBarrier(context.Context, func()) error
	}); ok {
		m.readBarrier = barrier.ReadBarrier
	}
	l.model = m
	d, err := source.ReadSnapshot(ctx)
	if err != nil {
		return err
	}
	m.publish(ctx, d)
	if m.failure != nil {
		return m.failure
	}
	source.ObserveReads(func(ctx context.Context, d historystore.LogsChanges) {
		m.mu.Lock()
		failed := m.failure != nil
		m.mu.Unlock()
		if d.Reset || failed {
			removal := d.RemovedBeforeMs
			if failed {
				removal = 0
			}
			if removal > 0 {
				m.removeBefore(removal)
			} else {
				m.revoke()
			}
			var err error
			rebuildCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			d, err = source.ReadSnapshot(rebuildCtx)
			cancel()
			if err != nil {
				m.mu.Lock()
				m.failure = err
				m.mu.Unlock()
				return
			}
			d.RemovedBeforeMs = removal
		}
		m.publish(ctx, d)
	})
	return nil
}

func (m *historyLogModel) revoke() {
	m.mu.Lock()
	if m.version != nil {
		m.version.valid.Store(false)
	}
	m.version = nil
	m.failure = errors.New("history_unavailable")
	m.mu.Unlock()
	if m.owner.reads != nil {
		m.owner.reads.invalidateLogModel()
	}
}

func (m *historyLogModel) publish(ctx context.Context, d historystore.LogsChanges) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		m.failure = err
		return
	}
	old := m.version
	v := &logModelVersion{meta: d.Meta, connections: map[string]*logModelConnection{}, events: map[string]*logModelEvent{}, members: map[string][]*logModelConnection{}, candidates: map[string][]*logModelEvent{}, valid: &atomic.Bool{}, removedBefore: &atomic.Int64{}, readBarrier: m.readBarrier}
	if old != nil && d.RemovedBeforeMs > 0 {
		v.valid, v.removedBefore = old.valid, old.removedBefore
	}
	if old != nil && !d.Reset {
		v.valid = old.valid
		v.removedBefore = old.removedBefore
		if len(d.Connections) == 0 && len(d.Events) == 0 {
			v.connections, v.events, v.ordered, v.candidates, v.members, v.bytes = old.connections, old.events, old.ordered, old.candidates, old.members, old.bytes
			v.activity = old.activity
			v.serial = old.serial
			m.version = v
			if m.owner.reads != nil {
				m.owner.reads.setCurrentLogModel(v)
			}
			m.failure = nil
			if m.owner.reads != nil {
				m.owner.reads.observeLogGenerations(d.Meta.LogDeletionGeneration, d.Meta.SummaryGeneration, d.Meta.JournalGeneration, d.Meta.LogRemovalBeforeMs)
			}
			return
		}
		for k, e := range old.connections {
			v.connections[k] = e
		}
		for k, e := range old.events {
			v.events[k] = e
		}
	}
	for key, row := range d.Connections {
		var s config.LogConnectionSummary
		if err := json.Unmarshal(row.Body, &s); err != nil {
			m.failure = err
			return
		}
		v.connections[key] = &logModelConnection{summary: s, bytes: len(row.Body) + 1024}
	}
	for _, row := range d.Events {
		e, err := logQueryEvent(row)
		if err != nil {
			m.failure = err
			return
		}
		v.events[row.EventID] = &logModelEvent{event: e, bytes: len(row.Body) + 1024}
	}
	// Merge the changed connection subset into the previous ordered directory.
	changed := make([]*logModelConnection, 0, len(d.Connections))
	for key := range d.Connections {
		changed = append(changed, v.connections[key])
	}
	lessConnection := func(a, b *logModelConnection) bool {
		af, bf := a.summary.Fact, b.summary.Fact
		if af.StartedAt.Equal(bf.StartedAt) {
			return af.Key < bf.Key
		}
		return af.StartedAt.After(bf.StartedAt)
	}
	sort.Slice(changed, func(i, j int) bool { return lessConnection(changed[i], changed[j]) })
	var kept []*logModelConnection
	if old != nil && !d.Reset {
		for _, e := range old.ordered {
			if _, updated := d.Connections[e.summary.Fact.Key]; !updated {
				kept = append(kept, e)
			}
		}
	}
	for i, j := 0, 0; i < len(kept) || j < len(changed); {
		if j == len(changed) || i < len(kept) && lessConnection(kept[i], changed[j]) {
			v.ordered = append(v.ordered, kept[i])
			i++
		} else {
			v.ordered = append(v.ordered, changed[j])
			j++
		}
	}
	// Only directories whose old/new members changed need a new slice.
	touched := map[string]bool{}
	connectionKeys := func(version *logModelVersion, e *logModelConnection) []string {
		var keys []string
		seen := map[string]bool{}
		identities := aggregateIdentities(version.flow(e, time.UnixMilli(1<<62)).Fact)
		identities = append(identities, aggregateIdentities(e.summary.Fact)...)
		for _, id := range identities {
			key := aggregateMemberKey(id)
			if !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
		}
		return keys
	}
	if old != nil && !d.Reset {
		for key, entries := range old.members {
			v.members[key] = entries
		}
	}
	for key := range d.Connections {
		if old != nil && !d.Reset {
			if e := old.connections[key]; e != nil {
				for _, k := range connectionKeys(old, e) {
					touched[k] = true
				}
			}
		}
		for _, k := range connectionKeys(v, v.connections[key]) {
			touched[k] = true
		}
	}
	addedByMember := map[string][]*logModelConnection{}
	for changedKey := range d.Connections {
		e := v.connections[changedKey]
		for _, key := range connectionKeys(v, e) {
			addedByMember[key] = append(addedByMember[key], e)
		}
	}
	for key := range touched {
		added := addedByMember[key]
		var previous []*logModelConnection
		sort.Slice(added, func(i, j int) bool { return lessConnection(added[i], added[j]) })
		if old != nil && !d.Reset {
			for _, e := range old.members[key] {
				if _, updated := d.Connections[e.summary.Fact.Key]; !updated {
					previous = append(previous, e)
				}
			}
		}
		merged := make([]*logModelConnection, 0, len(previous)+len(added))
		for i, j := 0, 0; i < len(previous) || j < len(added); {
			if j == len(added) || i < len(previous) && lessConnection(previous[i], added[j]) {
				merged = append(merged, previous[i])
				i++
			} else {
				merged = append(merged, added[j])
				j++
			}
		}
		v.members[key] = merged
	}
	for _, e := range v.ordered {
		v.bytes += e.bytes + 64
	}
	lessEvent := func(a, b *logModelEvent) bool {
		if a.event.At.Equal(b.event.At) {
			return a.event.ID > b.event.ID
		}
		return a.event.At.After(b.event.At)
	}
	for _, e := range v.events {
		v.bytes += e.bytes + 64
	}
	for _, view := range []string{"", "activity", "access", "problems", "operations"} {
		var added []*logModelEvent
		for _, row := range d.Events {
			e := v.events[row.EventID]
			if logViewKey(e.event, view) != "" {
				added = append(added, e)
			}
		}
		sort.Slice(added, func(i, j int) bool { return lessEvent(added[i], added[j]) })
		var previous []*logModelEvent
		if old != nil && !d.Reset {
			previous = old.candidates[view]
		}
		if len(added) == 0 {
			v.candidates[view] = previous
			continue
		}
		merged := make([]*logModelEvent, 0, len(previous)+len(added))
		for i, j := 0, 0; i < len(previous) || j < len(added); {
			if j == len(added) || i < len(previous) && lessEvent(previous[i], added[j]) {
				merged = append(merged, previous[i])
				i++
			} else {
				merged = append(merged, added[j])
				j++
			}
		}
		v.candidates[view] = merged
	}
	changedGroups := map[string]bool{}
	for key := range d.Connections {
		changedGroups[accessRecordID(key)] = true
	}
	var newEvents []config.HistoryEvent
	for _, row := range d.Events {
		event := v.events[row.EventID].event
		changedGroups[logViewKey(event, "activity")] = true
		newEvents = append(newEvents, event)
	}
	var previous *logModelVersion
	if old != nil && !d.Reset {
		previous = old
	}
	if err := v.buildActivity(ctx, previous, changedGroups, newEvents); err != nil {
		m.failure = err
		return
	}
	v.bytes += len(v.activity)*64 + len(v.ordered)*8
	for _, entries := range v.members {
		v.bytes += len(entries)*8 + 64
	}
	for _, entries := range v.candidates {
		v.bytes += len(entries)*8 + 64
	}
	if m.owner.reads != nil {
		m.owner.reads.trimExpired()
	}
	v.serial = m.serial + 1
	if m.owner.reads != nil {
		m.owner.reads.makeLogModelRoom(v)
	}
	if modelRetainedBytes(v, m.owner.reads) > logModelBudget {
		m.failure = errors.New("history_query_busy")
		return
	}
	m.serial = v.serial
	v.valid.Store(true)
	m.version = v
	if m.owner.reads != nil {
		m.owner.reads.setCurrentLogModel(v)
	}
	m.failure = nil
	if m.owner.reads != nil {
		m.owner.reads.observeLogGenerations(d.Meta.LogDeletionGeneration, d.Meta.SummaryGeneration, d.Meta.JournalGeneration, d.Meta.LogRemovalBeforeMs)
	}
}

func (l *historyLogs) readModel(ctx context.Context) (*logModelVersion, error) {
	if l.model == nil {
		return nil, nil
	}
	var v *logModelVersion
	var err error
	acquire := func() {
		l.model.mu.Lock()
		defer l.model.mu.Unlock()
		v, err = l.model.version, l.model.failure
		if v == nil && err == nil {
			err = errors.New("history_unavailable")
		}
	}
	if barrier, ok := l.logs.(interface {
		ReadBarrier(context.Context, func()) error
	}); ok {
		if e := barrier.ReadBarrier(ctx, acquire); e != nil {
			return nil, e
		}
	} else {
		acquire()
	}
	return v, err
}

func (cache *readTokenCache) invalidateLogModel() {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.dropLogEvidenceLocked()
	cache.modelCurrent = nil
}
func (cache *readTokenCache) trimExpired() {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for k, r := range cache.reads {
		if time.Since(r.timeUsed) > 2*time.Minute || cache.age(r) > 5*time.Minute {
			delete(cache.reads, k)
		}
	}
}

func (v *logModelVersion) flow(e *logModelConnection, at time.Time) stats.FlowWindow {
	s := e.summary
	f := s.Fact
	if f.Direction == "unknown" && f.RouteID == "" && f.Failure == "rule_rejected" {
		if event := v.events[s.LastEventID]; event != nil {
			rejected := event.event.Flow
			if rejected != nil && rejected.Key == f.Key && rejected.Direction == "blocked" && rejected.Failure == "rule_rejected" && !event.event.At.IsZero() && !event.event.At.After(at) {
				f.Direction, f.RouteID, f.RouteName = rejected.Direction, rejected.RouteID, rejected.RouteName
				f.Rule, f.RuleSource = rejected.Rule, rejected.RuleSource
			}
		}
	}
	if f.ObservedUntil != nil && f.ObservedUntil.After(at) || f.ClosedAt != nil && f.ClosedAt.After(at) {
		f.ClosedAt, f.ObservedUntil = nil, nil
		f.ResponseKnown, f.StateUnknown = false, true
		f.EstablishmentState, f.Failure, f.Phase = "", "", ""
	}
	return stats.FlowWindow{Fact: f, Upload: s.Metrics.Upload, Download: s.Metrics.Download}
}

// Prefix deletion revokes only reads whose selected time range can contain removed facts.
// The shared atomic also rejects late queries that acquired an older model before commit.
func (m *historyLogModel) removeBefore(before int64) {
	m.mu.Lock()
	if m.version != nil {
		m.version.removedBefore.Store(max(before, m.version.removedBefore.Load()))
	}
	m.mu.Unlock()
	if m.owner.reads != nil {
		m.owner.reads.removeLogsBefore(before)
	}
}
func (cache *readTokenCache) removeLogsBefore(before int64) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.dropLogEvidenceBeforeLocked(before)
}
