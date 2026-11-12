package controller

import (
	"context"
	"pax-connect/daemon/internal/historystore"
	"strconv"
	"time"
)

// publishState hands readers an immutable traffic-owned status, independently of logs.db.
func (t *historyTraffic) publishState(ctx context.Context) {
	m := historyMetadata{Revision: t.state.revision, TrafficRevision: t.state.revision, TrafficDeletionGeneration: t.state.deletion}
	for _, field := range []struct {
		key  string
		dest *bool
	}{{historystore.MetaPartial, &m.Partial}, {historystore.MetaCapacityEvicted, &m.CapacityEvicted}} {
		v, _, err := t.store.Meta(ctx, field.key)
		if err == nil {
			*field.dest = v == "1"
		}
	}
	// Retain incomplete-import evidence from older SQLite stores; never start an import.
	if raw, _, err := t.store.Meta(ctx, "migration_pending"); err == nil && raw == "1" {
		m.Partial = true
	}
	m.Error, _, _ = t.store.Meta(ctx, historystore.MetaErrorCode)
	for _, key := range []string{trafficMetaCoverageFrom, "evicted_before"} {
		raw, _, e := t.store.Meta(ctx, key)
		if e == nil {
			n, _ := strconv.ParseInt(raw, 10, 64)
			if n > 0 {
				m.Coverage = maxTime(m.Coverage, time.UnixMilli(n))
			}
		}
	}
	if t.down {
		m.Partial = true
		m.Error = trafficErrorStore
	}
	// Signal the publisher only when what readers see moved; an idle pass changes nothing.
	prev := t.meta.Swap(&m)
	if t.changed != nil && (prev == nil || *prev != m) {
		t.changed()
	}
}
