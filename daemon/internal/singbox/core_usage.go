package singbox

import (
	"context"
	"pax-connect/daemon/internal/config"
	"time"
)

// ReadUsage is a compatibility projection of the observation owner. It never samples twice.
func (r *CoreRuntime) ReadUsage(_ context.Context) config.UsageSnapshot {
	o := r.observations
	o.mu.Lock()
	defer o.mu.Unlock()
	out := config.UsageSnapshot{Session: o.session, Sequence: o.sequence, At: time.Now(), Partial: o.lostBefore > 0, ConnectionsKnown: o.collecting.Load()}

	totals := map[string]config.UsageCounter{}
	for tag, total := range o.totals {
		totals[tag] = total
	}
	for _, f := range o.flows {
		total := totals[f.fact.RouteID]
		total.Tag = f.fact.RouteID
		total.Upload += f.up
		total.Download += f.down
		if f.fact.ClosedAt == nil {
			total.Connections++
		}
		totals[total.Tag] = total
	}
	for _, total := range totals {
		out.Counters = append(out.Counters, total)
	}

	return out
}
