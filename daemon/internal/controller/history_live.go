package controller

import (
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
	"sync"
	"time"
)

const (
	// liveBucket and liveWindowPoints give the 60 s speed view: 12 points of 5 s (history-storage §4.4).
	liveBucket       = 5 * time.Second
	liveWindowPoints = 12
)

// livePoint is the traffic of one 5 s bucket.
type livePoint struct{ Upload, Download uint64 }

// liveFlow is one active connection: the latest fact plus bytes accumulated since it was first seen here.
type liveFlow struct {
	Fact             config.FlowFact
	Upload, Download uint64
	LastAt           time.Time
	// buckets is the flow's last 60 s of bytes in 5 s points, keyed like liveState.buckets.
	buckets map[int64]livePoint
}

func (f *liveFlow) addBytes(id int64, up, down uint64) {
	if up == 0 && down == 0 {
		return
	}
	if f.buckets == nil {
		f.buckets = map[int64]livePoint{}
	}
	p := f.buckets[id]
	p.Upload += up
	p.Download += down
	f.buckets[id] = p
}

// liveSnapshot is an immutable copy for readers. Window is oldest first and its last
// point is the last completed 5 s bucket: the one ending at the latest 5 s boundary
// at or before the snapshot time.
type liveSnapshot struct {
	// Known mirrors the old rule: the count only means something while the core is collecting
	// and the active set has been seeded from a baseline.
	Known  bool
	Active int
	Window [liveWindowPoints]livePoint
}

// liveState is the in-memory real-time view fed by the ingest goroutine (single writer).
// Its (session, seq) watermark is independent of the journal cursor: records re-pulled after
// a journal failure are at or below the watermark and are skipped, never counted twice.
type liveState struct {
	mu         sync.RWMutex
	flows      map[string]*liveFlow
	closed     map[string]*liveFlow // flows closed within the last minute, for the per-flow view
	marks      map[string]flowMarks // counting marks of recent flows, advanced like traffic.db's
	pending    pendingLog           // totals after the traffic job's cursor
	seeding    map[string]*liveFlow // baseline being collected; nil when not seeding
	seeded     bool
	mark       historystore.Cursor
	collecting bool
	active     int
	buckets    map[int64]livePoint
}

func newLiveState() *liveState {
	return &liveState{flows: map[string]*liveFlow{}, closed: map[string]*liveFlow{}, marks: map[string]flowMarks{}, buckets: map[int64]livePoint{}}
}

// apply folds one core batch into the state. now only bounds how many buckets are kept.
func (l *liveState) apply(b config.ObservationBatch, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.collecting = b.Collecting
	if b.ResetToken != "" {
		l.seed(b)
	}
	for _, o := range b.Records {
		l.record(b.Session, o)
	}
	l.recount()
	l.prune(now)
}

// seed handles one page of a frozen baseline. The first page (ResetRequired) discards the
// previous active set: after a gap it can no longer be trusted. The new set replaces it
// only when the last page arrives, and the watermark moves to the baseline high water.
func (l *liveState) seed(b config.ObservationBatch) {
	if b.ResetRequired || l.seeding == nil {
		l.seeding = map[string]*liveFlow{}
		l.flows = map[string]*liveFlow{}
		l.seeded = false
	}
	for _, f := range b.Baseline {
		l.seeding[f.Key] = &liveFlow{Fact: f}
	}
	if !b.BaselineComplete {
		return
	}
	l.flows, l.seeding, l.seeded = l.seeding, nil, true
	if l.mark.Session != b.Session {
		l.mark = historystore.Cursor{Session: b.Session}
	}
	l.mark.Seq = max(l.mark.Seq, b.BaselineHighWater)
}

func (l *liveState) record(session string, o config.Observation) {
	if o.Session != session {
		return
	}
	if l.mark.Session != session {
		l.mark = historystore.Cursor{Session: session}
		l.flows = map[string]*liveFlow{}
	}
	if o.Sequence <= l.mark.Seq {
		return
	}
	l.mark.Seq = o.Sequence
	if o.Flow == nil {
		return
	}
	fact := *o.Flow
	id := bucketID(o.At)
	if o.Upload != 0 || o.Download != 0 {
		if !config.IsDNSControl(fact) {
			p := l.buckets[id]
			p.Upload += o.Upload
			p.Download += o.Download
			l.buckets[id] = p
		}
	}
	if !o.At.IsZero() {
		l.track(o)
	}
	f := l.flows[fact.Key]
	if f == nil {
		f = l.closed[fact.Key]
	}
	if f == nil {
		f = &liveFlow{}
	}
	f.Fact = fact
	f.Upload += o.Upload
	f.Download += o.Download
	f.addBytes(id, o.Upload, o.Download)
	f.LastAt = o.At
	if fact.ClosedAt != nil {
		delete(l.flows, fact.Key)
		l.closed[fact.Key] = f
		return
	}
	delete(l.closed, fact.Key)
	l.flows[fact.Key] = f
}

// recount applies the Watch rule of historyToday: not closed, not state-unknown, not DNS control.
func (l *liveState) recount() {
	l.active = 0
	for _, f := range l.flows {
		if f.Fact.ClosedAt == nil && !f.Fact.StateUnknown && !config.IsDNSControl(f.Fact) {
			l.active++
		}
	}
}

// bucketID is the end boundary of the 5 s bucket containing at (a boundary belongs to the bucket it ends).
func bucketID(at time.Time) int64 {
	return (at.UnixNano() + int64(liveBucket) - 1) / int64(liveBucket)
}

func (l *liveState) prune(now time.Time) {
	oldest := bucketID(now) - liveWindowPoints
	for id := range l.buckets {
		if id < oldest {
			delete(l.buckets, id)
		}
	}
	for key, f := range l.closed {
		if now.Sub(f.LastAt) > closedFlowAge {
			delete(l.closed, key)
		}
	}
	for _, flows := range []map[string]*liveFlow{l.flows, l.closed} {
		for _, f := range flows {
			for id := range f.buckets {
				if id < oldest {
					delete(f.buckets, id)
				}
			}
		}
	}
	l.pruneMarks(now)
	l.compactPending(now)
}

// disconnect records that the core could not be read, so the active count is no longer known.
func (l *liveState) disconnect() {
	l.mu.Lock()
	l.collecting = false
	l.mu.Unlock()
}

// Snapshot returns the current view; the window ends at the last full bucket before now.
func (l *liveState) Snapshot(now time.Time) liveSnapshot {
	l.mu.RLock()
	defer l.mu.RUnlock()
	s := liveSnapshot{Known: l.collecting && l.seeded, Active: l.active}
	last := now.UnixNano() / int64(liveBucket)
	for i := range s.Window {
		s.Window[i] = l.buckets[last-int64(liveWindowPoints-1-i)]
	}
	return s
}
