package controller

import (
	"sort"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

const (
	// pendingDetailAge is how long pending entries keep their own record. A job that falls further
	// behind only keeps totals, and the delta is reported partial (history-storage §4.4).
	pendingDetailAge = 30 * time.Minute
	// closedFlowAge is how long a closed flow stays in the per-flow view after its last record.
	closedFlowAge = time.Minute
	// pendingMaxEntries bounds the memory of a job that lags behind a burst: the oldest entries beyond it
	// fold into the totals-only part like those older than pendingDetailAge.
	pendingMaxEntries = 50000
)

// Route classes of a pending entry, as historyToday splits today's bytes.
const (
	pendingProxied = iota
	pendingDirect
	pendingUnclassified
)

// pendingEntry is the effect of one core observation on today's totals: bytes by route class and the
// one-shot counters, with the (session, seq) the traffic job's commit is measured against.
type pendingEntry struct {
	kind           string
	fact           *config.FlowFact
	count          flowCount
	session        string
	seq            uint64
	at             time.Time
	up, down       uint64
	class          int
	starts, failed uint64
}

// trafficPending is the Watch-today delta not yet committed to traffic.db: the totals of every
// observation after the traffic job's cursor. DNS control flows are excluded, as in route usage.
type trafficPending struct {
	Upload, Download              uint64
	Direct, Proxied, Unclassified uint64
	Connections, Failed           uint64
	// Partial reports that old entries were folded into totals because the job lags by more than 30 minutes.
	Partial bool
}

func (p *trafficPending) add(e pendingEntry) {
	p.Upload += e.up
	p.Download += e.down
	p.Connections += e.starts
	p.Failed += e.failed
	switch e.class {
	case pendingDirect:
		p.Direct += e.up + e.down
	case pendingUnclassified:
		p.Unclassified += e.up + e.down
	default:
		p.Proxied += e.up + e.down
	}
}

// pendingOf derives the entry of one observation with the traffic job's counting predicate. ok is
// false when it changes no total; next are the marks after the observation either way.
func pendingOf(m flowMarks, o config.Observation) (entry pendingEntry, next flowMarks, ok bool) {
	f := *o.Flow
	c := countFlow(m, o, f)
	next = m.advance(c, o, f)

	fact := cloneFlowFact(f)
	entry = pendingEntry{kind: o.Kind, fact: &fact, count: c, session: o.Session, seq: o.Sequence, at: o.At, up: o.Upload, down: o.Download, starts: b2u(c.Start), failed: b2u(c.Failed)}
	switch {
	case f.Direction == "direct" || f.RouteID == "direct": // mapHistoryRoute turns a direct flow into route "direct"
		entry.class = pendingDirect
	case f.RouteID == "" || f.RouteID == "unattributed":
		entry.class = pendingUnclassified
	}
	if config.IsDNSControl(f) {
		entry.up, entry.down, entry.starts, entry.failed = 0, 0, 0, 0
	}
	return entry, next, entry.up != 0 || entry.down != 0 || entry.starts != 0 || entry.failed != 0
}

func b2u(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}

// pendingLog holds the uncommitted entries in stream order. Entries older than pendingDetailAge are
// folded into compact, which is dropped once the job commits past compactThrough.
type pendingLog struct {
	entries        []pendingEntry
	attributed     []pendingEntry // immutable until pending changes
	compactFrom    historystore.Cursor
	compactStarted bool
	compactDays    map[string]trafficPending
	compact        trafficPending
	hasCompact     bool
	compactThrough historystore.Cursor
}

// track folds one observation's effect into the pending delta and advances the live marks.
func (l *liveState) track(o config.Observation) {
	l.pending.attributed = nil
	key := o.Flow.Key
	entry, next, ok := pendingOf(l.marks[key], o)
	l.marks[key] = next
	if ok || entry.fact != nil {
		l.pending.entries = append(l.pending.entries, entry)
		if over := len(l.pending.entries) - pendingMaxEntries; over > 0 {
			// Fold a tenth extra so a sustained burst copies the slice once per 5,000 records, not every one.
			l.pending.fold(over + pendingMaxEntries/10)
		}
	}
}

// commitTraffic drops the pending entries the traffic job has committed: those of the core session at
// or before core, and everything that precedes them in stream order.
func (l *liveState) commitTraffic(core historystore.Cursor, receipts ...pendingEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p := &l.pending
	p.attributed = nil
	if p.hasCompact && len(receipts) > 0 {
		for _, e := range receipts {
			if !p.compactStarted {
				if e.session != p.compactFrom.Session || e.seq < p.compactFrom.Seq {
					continue
				}
				p.compactStarted = true
			}
			subtract := func(v *trafficPending) {
				v.Upload -= min(v.Upload, e.up)
				v.Download -= min(v.Download, e.down)
				v.Connections -= min(v.Connections, e.starts)
				v.Failed -= min(v.Failed, e.failed)
				v.Unclassified -= min(v.Unclassified, e.up+e.down)
			}
			subtract(&p.compact)
			day := e.at.In(time.Local).Format("2006-01-02")
			v := p.compactDays[day]
			subtract(&v)
			p.compactDays[day] = v
			if e.session == p.compactThrough.Session && e.seq >= p.compactThrough.Seq {
				p.compactStarted = false
				break
			}
		}
	}
	last := -1
	for i, e := range p.entries {
		if e.session == core.Session && e.seq <= core.Seq {
			last = i
		}
	}
	if last >= 0 {
		p.entries = append([]pendingEntry(nil), p.entries[last+1:]...)
	}
	if p.hasCompact && (last >= 0 || p.compactThrough.Session == core.Session && p.compactThrough.Seq <= core.Seq) {
		p.compact, p.hasCompact = trafficPending{}, false
		p.compactDays = nil
		p.compactStarted = false
		p.compactFrom = historystore.Cursor{}
	}
}

// fold moves the oldest n entries into the totals-only part and marks the delta partial.
func (p *pendingLog) fold(n int) {
	p.attributed = nil
	attributed := pendingAttributed(p.entries)
	if !p.hasCompact && len(attributed) > 0 {
		p.compactFrom = historystore.Cursor{Session: attributed[0].session, Seq: attributed[0].seq}
	}
	if p.compactDays == nil {
		p.compactDays = map[string]trafficPending{}
	}
	for _, e := range attributed[:n] {
		e.class = pendingUnclassified
		day := e.at.In(time.Local).Format("2006-01-02")
		v := p.compactDays[day]
		v.add(e)
		p.compactDays[day] = v
		p.compact.add(e)
		p.compactThrough = historystore.Cursor{Session: e.session, Seq: e.seq}
	}
	p.hasCompact = true
	p.entries = append([]pendingEntry(nil), p.entries[n:]...)
}

// compactPending folds entries older than pendingDetailAge into the totals-only part.
func (l *liveState) compactPending(now time.Time) {
	p := &l.pending
	cut := 0
	for cut < len(p.entries) && now.Sub(p.entries[cut].at) > pendingDetailAge {
		cut++
	}
	if cut > 0 {
		p.fold(cut)
	}
}

// pruneMarks drops marks that can no longer matter: closed flows 10 minutes after their last record
// and never-closed ones after a day (the same rule as flow_marks, on observation time).
func (l *liveState) pruneMarks(now time.Time) {
	for key, m := range l.marks {
		if m.Closed && now.Sub(m.LastAt) > trafficClosedMarkAge || now.Sub(m.LastAt) > trafficOpenMarkAge {
			delete(l.marks, key)
		}
	}
}

// Pending returns today's uncommitted totals.
func (l *liveState) Pending() trafficPending {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.pending.compact
	out.Partial = l.pending.hasCompact
	for _, e := range l.attributedPending() {
		out.add(e)
	}
	return out
}

// restorePending rebases the complete uncommitted journal/live tail on the durable
// counting marks. Duplicate cursor records are removed before counters are replayed.
func (l *liveState) restorePending(entries []pendingEntry, marks map[string]flowMarks) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// The database marks are authoritative. Re-derive every uncommitted record,
	// including records capture admitted while startup recovery was running.
	seen := map[historystore.Cursor]bool{}
	merged := make([]pendingEntry, 0, len(entries)+len(l.pending.entries))
	for _, group := range [][]pendingEntry{entries, l.pending.entries} {
		for _, e := range group {
			key := historystore.Cursor{Session: e.session, Seq: e.seq}
			if seen[key] {
				continue
			}
			seen[key] = true
			merged = append(merged, e)
		}
	}
	l.pending = pendingLog{}
	for _, e := range merged {
		if e.fact == nil {
			continue
		}
		o := config.Observation{Session: e.session, Sequence: e.seq, At: e.at, Kind: e.kind, Flow: e.fact, Upload: e.up, Download: e.down}
		entry, next, _ := pendingOf(marks[e.fact.Key], o)
		marks[e.fact.Key] = next
		l.pending.entries = append(l.pending.entries, entry)
	}
	l.marks = marks
	if len(l.pending.entries) > pendingMaxEntries {
		l.pending.fold(len(l.pending.entries) - pendingMaxEntries)
	}
}

// liveFlowView is an immutable per-flow copy: the latest fact, the bytes accumulated since the flow
// was first seen here, and its last 60 s in 5 s points, oldest first.
type liveFlowView struct {
	Fact             config.FlowFact
	Upload, Download uint64
	Window           [liveWindowPoints]livePoint
	Closed           bool
}

// cloneFlowFact copies the pointer and slice fields of a fact, so a view never shares memory with the
// state it was taken from or with the observation the state was fed from.
func cloneFlowFact(f config.FlowFact) config.FlowFact {
	if f.ClosedAt != nil {
		t := *f.ClosedAt
		f.ClosedAt = &t
	}
	if f.ObservedUntil != nil {
		t := *f.ObservedUntil
		f.ObservedUntil = &t
	}
	if f.DNS != nil {
		d := *f.DNS
		d.Answers = append([]string(nil), d.Answers...)
		f.DNS = &d
	}
	return f
}

// Flows returns every active flow and every flow closed within the last minute, sorted by key. The
// window ends at the last completed 5 s bucket, like Snapshot.
func (l *liveState) Flows(now time.Time) []liveFlowView {
	l.mu.RLock()
	defer l.mu.RUnlock()
	last := now.UnixNano() / int64(liveBucket)
	out := make([]liveFlowView, 0, len(l.flows)+len(l.closed))
	add := func(f *liveFlow) {
		v := liveFlowView{Fact: cloneFlowFact(f.Fact), Upload: f.Upload, Download: f.Download, Closed: f.Fact.ClosedAt != nil}
		for i := range v.Window {
			v.Window[i] = f.buckets[last-int64(liveWindowPoints-1-i)]
		}
		out = append(out, v)
	}
	for _, f := range l.flows {
		add(f)
	}
	for _, f := range l.closed {
		add(f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Fact.Key < out[j].Fact.Key })
	return out
}
