package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

const (
	trafficBucket5m = 5 * time.Minute
	trafficBucket5s = 5 * time.Second
	// coverageBytes holds the 300 one-second bits of a 5-minute bucket (5 little-endian words).
	coverageBytes = 40
)

// trafficBatch accumulates one transaction's merges in memory: each row is read from the database at
// most once, merged with every observation of the batch and written back once. All of it is
// discarded when the transaction fails.
type trafficBatch struct {
	receipts []pendingEntry
	searches [2]map[historystore.TrafficKey]*trafficSearchIndex
	tx       *historystore.TrafficTx
	rows     [2]map[historystore.TrafficKey]*aggregateBucket // traffic_5m, recent_cuts
	marks    map[string]*markEntry
	coverage map[int64][]byte
}

type markEntry struct {
	mark    flowMarks
	existed bool
}

func newTrafficBatch(tx *historystore.TrafficTx) *trafficBatch {
	return &trafficBatch{searches: [2]map[historystore.TrafficKey]*trafficSearchIndex{{}, {}}, tx: tx, marks: map[string]*markEntry{}, coverage: map[int64][]byte{},
		rows: [2]map[historystore.TrafficKey]*aggregateBucket{{}, {}}}
}

// mark returns the connection's counting mark, loading it on first use.
func (b *trafficBatch) mark(ctx context.Context, key string) (*markEntry, error) {
	if e := b.marks[key]; e != nil {
		return e, nil
	}
	m, ok, err := b.tx.FlowMark(ctx, key)
	if err != nil {
		return nil, err
	}
	e := &markEntry{existed: ok}
	if ok {
		e.mark = flowMarks{Started: m.Started, Failed: m.Failed, Responded: m.Responded, Closed: m.Closed, LastAt: time.UnixMilli(m.LastAtMs).UTC()}
	}
	b.marks[key] = e
	return e, nil
}

// merge applies one write to the row of the 5-minute (table 0) or millisecond (table 1) bucket of o.At.
// It follows the historical write() of acceptAggregate: bytes and counters add up, latency feeds the histogram,
// maximum and latest, and the rule basis gains one observation.
func (b *trafficBatch) merge(ctx context.Context, table int, w trafficWrite, f config.FlowFact, o config.Observation) error {
	step, name := trafficBucket5m, "traffic_5m"
	if table == 1 {
		name = "recent_cuts"
	}
	at := o.At.UTC().Truncate(step)
	if table == 1 {
		at = o.At.UTC().Truncate(time.Millisecond)
	}

	key := historystore.TrafficKey{BucketMs: at.UnixMilli(), Dimension: w.id.Dimension, Object: w.id.Object, Route: w.id.Route, Direction: w.id.Direction}
	v := b.rows[table][key]
	if v == nil {
		var row historystore.TrafficRow
		var ok bool
		var err error
		switch table {
		case 0:
			row, ok, err = b.tx.Traffic5m(ctx, key)
		case 1:
			row, ok, err = b.tx.RecentCut(ctx, key)
		}
		if err != nil {
			return err
		}
		v = &aggregateBucket{}
		if ok {
			if *v, err = decodeTrafficRow(row); err != nil {
				return fmt.Errorf("history traffic: decode %s: %w", name, err)
			}
		}
		b.rows[table][key] = v
	}
	if v.Identity.Dimension != "" {
		// The row key has no source kind: sources that reach one object merge into one row.
		w.id.Kind = betterSourceKind(v.Identity.Kind, w.id.Kind)
	}
	index := b.searches[table][key]
	if index == nil {
		index = newTrafficSearchIndex(v.Identity.Search)
		b.searches[table][key] = index
	}
	index.add(w.id.Search)
	v.Identity = w.id
	v.At = at
	v.Upload += w.up
	v.Download += w.down
	v.Connections += w.starts
	v.Failed += w.failures
	bases := flowAggregateBases(f, w.starts)
	if summarizesTrafficDimension(w.id.Dimension) {
		bases = observedRuleBasis(f, w, o)
	}
	mergeAggregateBases(v, bases)
	if w.withLatency {
		if v.Latency == nil {
			v.Latency = map[int]uint64{}
		}
		v.Latency[latencyBin(w.latency)]++
		v.Maximum = max(v.Maximum, w.latency)
		if !o.At.Before(v.LatestAt) {
			v.Latest = w.latency
			v.LatestAt = o.At
		}
	}
	return nil
}

// flush writes every merged row, mark and coverage word back.
func (b *trafficBatch) flush(ctx context.Context) error {
	for table, rows := range b.rows {
		for key, v := range rows {
			for _, text := range b.searches[table][key].parts {
				if err := b.tx.PutSearchEvidence(ctx, table, key, text); err != nil {
					return err
				}
			}
			// Keep the row's latest evidence for old readers; accumulated evidence is append-only.
			row, err := encodeTrafficRow(key, *v)
			if err != nil {
				return err
			}
			switch table {
			case 0:
				err = b.tx.PutTraffic5m(ctx, row)
			case 1:
				err = b.tx.PutRecentCut(ctx, row)
			}
			if err != nil {
				return err
			}
		}
	}
	for key, e := range b.marks {
		if !e.existed && !e.mark.active() && !e.mark.Closed {
			continue // nothing to deduplicate yet
		}
		m := historystore.FlowMark{FlowKey: key, Started: e.mark.Started, Failed: e.mark.Failed, Responded: e.mark.Responded,
			Closed: e.mark.Closed, LastAtMs: e.mark.LastAt.UnixMilli()}
		if err := b.tx.PutFlowMark(ctx, m); err != nil {
			return err
		}
	}
	for bucket, bits := range b.coverage {
		if err := b.tx.PutCoverage(ctx, historystore.Coverage{BucketMs: bucket, Bits: bits}); err != nil {
			return err
		}
	}
	return nil
}

// markSeconds sets the one-second collection bits of [from, until) in their 5-minute buckets.
func (b *trafficBatch) markSeconds(ctx context.Context, from, until time.Time) error {
	for at := from.Truncate(time.Second); at.Before(until.Truncate(time.Second)); at = at.Add(time.Second) {
		base := at.Truncate(trafficBucket5m)
		bits, ok := b.coverage[base.UnixMilli()]
		if !ok {
			c, found, err := b.tx.Coverage(ctx, base.UnixMilli())
			if err != nil {
				return err
			}
			bits = make([]byte, coverageBytes)
			if found {
				copy(bits, c.Bits)
			}
			b.coverage[base.UnixMilli()] = bits
		}
		offset := int(at.Sub(base) / time.Second)
		bits[offset/8] |= 1 << uint(offset%8)
	}
	return nil
}

// sourceRank orders source kinds for a merged row: a concrete owner beats a guess.
var sourceRank = map[string]int{"app": 0, "tool": 1, "system": 2, "unknown": 3, "dns": 4}

func betterSourceKind(a, b string) string {
	ra, okA := sourceRank[a]
	rb, okB := sourceRank[b]
	if okA && (!okB || ra <= rb) {
		return a
	}
	return b
}

func unixMilliOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func encodeTrafficRow(key historystore.TrafficKey, v aggregateBucket) (historystore.TrafficRow, error) {
	row := historystore.TrafficRow{TrafficKey: key, Name: v.Identity.Name, SourceKind: v.Identity.Kind, RouteName: v.Identity.RouteName,
		UpBytes: int64(v.Upload), DownBytes: int64(v.Download), NewConns: int64(v.Connections), Failures: int64(v.Failed),
		LatencyMs: int64(v.Latest), LatencyAtMs: unixMilliOrZero(v.LatestAt), MaxLatencyMs: int64(v.Maximum), Search: v.Identity.Search}
	var err error
	if len(v.Latency) > 0 {
		if row.LatencyHist, err = json.Marshal(v.Latency); err != nil {
			return row, fmt.Errorf("history traffic: encode histogram: %w", err)
		}
	}
	if len(v.Bases) > 0 {
		if row.RuleBasis, err = json.Marshal(v.Bases); err != nil {
			return row, fmt.Errorf("history traffic: encode basis: %w", err)
		}
	}
	return row, nil
}

// decodeTrafficRow is the inverse of encodeTrafficRow, at millisecond time resolution.
func decodeTrafficRow(r historystore.TrafficRow) (aggregateBucket, error) {
	v := aggregateBucket{LegacyGap: r.LegacyGap, At: time.UnixMilli(r.BucketMs).UTC(), Upload: uint64(r.UpBytes), Download: uint64(r.DownBytes),
		Connections: uint64(r.NewConns), Failed: uint64(r.Failures), Latest: uint64(r.LatencyMs), Maximum: uint64(r.MaxLatencyMs),
		Identity: aggregateIdentity{Dimension: r.Dimension, Object: r.Object, Name: r.Name, Kind: r.SourceKind, Route: r.Route,
			RouteName: r.RouteName, Direction: r.Direction, Search: r.Search}}
	if r.LatencyAtMs != 0 {
		v.LatestAt = time.UnixMilli(r.LatencyAtMs).UTC()
	}
	if len(r.LatencyHist) > 0 {
		if err := json.Unmarshal(r.LatencyHist, &v.Latency); err != nil {
			return v, err
		}
	}
	if len(r.RuleBasis) > 0 {
		if err := json.Unmarshal(r.RuleBasis, &v.Bases); err != nil {
			return v, err
		}
	}
	trackLegacyMatches(&v)
	return v, nil
}

func unixMilliTime(ms int64) time.Time { return time.UnixMilli(ms).UTC() }
