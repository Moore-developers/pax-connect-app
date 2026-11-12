package singbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"pax-connect/daemon/internal/config"
	"strconv"
	"time"
)

type activeBaseline struct {
	token     string
	at        time.Time
	highWater uint64
	flows     []config.FlowFact
}

// ReadObservationPage freezes active identities after a lost sequence range.
// It does not reconstruct bytes or events that were never delivered.
func (r *CoreRuntime) ReadObservationPage(ctx context.Context, q config.ObservationRead) config.ObservationBatch {
	o := r.observations
	o.mu.Lock()
	reset := q.Session != "" && q.Session != o.session || q.After > o.sequence || q.After+1 < o.lostBefore
	if !reset && q.ResetToken == "" {
		o.mu.Unlock()
		return r.ReadObservations(ctx, q.Session, q.After, q.Limit)
	}
	restart := q.ResetToken == "" || o.baseline == nil || o.baseline.token != q.ResetToken || time.Since(o.baseline.at) > 5*time.Second
	if restart {
		now := time.Now()
		baseline := &activeBaseline{at: now, highWater: o.sequence}
		var token [16]byte
		if _, e := rand.Read(token[:]); e != nil {
			o.mu.Unlock()
			return config.ObservationBatch{Session: o.session, ResetRequired: true}
		}
		baseline.token = hex.EncodeToString(token[:])
		for _, f := range o.flows {
			baseline.flows = append(baseline.flows, f.fact)
		}
		o.baseline = baseline
	}
	baseline := o.baseline
	offset := 0
	if !restart {
		n, e := strconv.Atoi(q.Cursor)
		if e == nil && n >= 0 && n <= len(baseline.flows) {
			offset = n
		} else {
			restart = true
		}
	}
	if restart {
		offset = 0
	}
	limit := q.Limit
	if limit <= 0 || limit > 256 {
		limit = 256
	}
	end := min(len(baseline.flows), offset+limit)
	b := config.ObservationBatch{Session: o.session, HighWater: o.sequence, LostBefore: o.lostBefore, Collecting: o.collecting.Load(), ResetRequired: offset == 0, Baseline: append([]config.FlowFact(nil), baseline.flows[offset:end]...), ResetToken: baseline.token, BaselineHighWater: baseline.highWater, BaselineComplete: end == len(baseline.flows)}
	if end < len(baseline.flows) {
		b.NextCursor = strconv.Itoa(end)
	}
	o.mu.Unlock()
	return b
}
