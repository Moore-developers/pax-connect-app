package controller

import (
	"context"
	"encoding/binary"
	"errors"
	"strconv"
	"time"

	"pax-connect/daemon/internal/cadence"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// trafficSnapshot owns a pinned database version and the exact memory tail after it.
// The job excludes commits only while pinning and copying, never while projecting a query.
type trafficSnapshot struct {
	read     *historystore.TrafficRead
	entries  []pendingEntry
	compact  trafficPending
	meta     historyMetadata
	coverage time.Time
}

func (c *Controller) trafficSnapshot(ctx context.Context) (*trafficSnapshot, error) {
	t := c.trafficJob()
	if t == nil {
		return nil, errors.New("history_unavailable")
	}
	for !t.snapshotMu.TryRLock() {
		if !cadence.Wait(ctx, time.Millisecond) {
			return nil, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		t.snapshotMu.RUnlock()
		return nil, err
	}
	read, err := t.store.BeginRead(ctx)
	if err != nil {
		t.snapshotMu.RUnlock()
		return nil, err
	}
	s := &trafficSnapshot{read: read}
	if live := t.liveView(); live != nil {
		s.entries, s.compact = live.pendingSnapshot()
	}
	t.snapshotMu.RUnlock()
	s.entries, err = t.resolvePending(ctx, s.entries)
	if err != nil {
		_ = read.Close()
		return nil, err
	}

	s.meta = c.publishedTrafficMeta()
	s.meta.Collection = "idle"
	for _, field := range []struct {
		key  string
		dest *uint64
	}{{"traffic_revision", &s.meta.TrafficRevision}} {
		v, _, e := read.Meta(ctx, field.key)
		if e != nil {
			_ = read.Close()
			return nil, e
		}
		*field.dest, _ = strconv.ParseUint(v, 10, 64)
	}
	for _, field := range []struct {
		key  string
		dest *bool
	}{{historystore.MetaPartial, &s.meta.Partial}, {historystore.MetaCapacityEvicted, &s.meta.CapacityEvicted}} {
		v, _, e := read.Meta(ctx, field.key)
		if e != nil {
			_ = read.Close()
			return nil, e
		}
		*field.dest = v == "1"
	}
	// Retain incomplete-import evidence from older SQLite stores; never start an import.
	if raw, _, e := read.Meta(ctx, "migration_pending"); e == nil && raw == "1" {
		s.meta.Partial = true
	}
	s.meta.Error, _, err = read.Meta(ctx, historystore.MetaErrorCode)
	if err != nil {
		_ = read.Close()
		return nil, err
	}
	for _, key := range []string{trafficMetaCoverageFrom, "evicted_before"} {
		v, _, e := read.Meta(ctx, key)
		if e != nil {
			_ = read.Close()
			return nil, e
		}
		ms, _ := strconv.ParseInt(v, 10, 64)
		if ms > 0 {
			at := time.UnixMilli(ms)
			if at.After(s.coverage) {
				s.coverage = at
			}
		}
	}
	if live := t.liveView(); live != nil {
		if live.Snapshot(time.Now()).Known {
			s.meta.Collection = "collecting"
		} else {
			s.meta.Collection = "idle"
		}
	}
	return s, nil
}

func (s *trafficSnapshot) coverageRows(ctx context.Context, from, until time.Time) (map[int64]aggregateCoverage, error) {
	out := map[int64]aggregateCoverage{}
	err := s.read.Coverage(ctx, from, until, func(row historystore.Coverage) error {
		v := aggregateCoverage{At: time.UnixMilli(row.BucketMs)}
		for i := range v.Bits {
			if len(row.Bits) >= (i+1)*8 {
				v.Bits[i] = binary.LittleEndian.Uint64(row.Bits[i*8:])
			}
		}
		out[v.At.Unix()] = v
		return nil
	})
	return out, err
}

func (s *trafficSnapshot) buckets(ctx context.Context, from, until time.Time, dimensions []string) ([]aggregateBucket, error) {
	var out []aggregateBucket
	size := 0
	err := s.eachBucket(ctx, from, until, dimensions, func(v aggregateBucket) error {
		size += len(v.Identity.Search) + len(v.Identity.Name) + 512 + len(v.Latency)*64
		if size > 16<<20 {
			return errors.New("history_query_busy")
		}
		out = append(out, v)
		return nil
	})
	return out, err
}

// eachBucket projects a bounded SQL stream without retaining raw month-sized input.
func (s *trafficSnapshot) eachBucket(ctx context.Context, from, until time.Time, dimensions []string, consume func(aggregateBucket) error) error {
	add := func(row historystore.TrafficRow) error {
		v, err := decodeTrafficRow(row)
		if err != nil {
			return err
		}
		v.Identity = projectStoredIdentity(v.Identity)
		return consume(v)
	}

	boundary := until.Truncate(trafficBucket5m)
	if err := s.read.Rows(ctx, false, from.Truncate(trafficBucket5m), boundary, dimensions, add); err != nil {
		return err
	}
	if until.After(boundary) {
		if until.Before(time.Now().Add(-10 * time.Minute)) {
			if err := s.read.Rows(ctx, false, boundary, boundary.Add(trafficBucket5m), dimensions, add); err != nil {
				return err
			}
		} else if err := s.read.Rows(ctx, true, boundary, until, dimensions, add); err != nil {
			return err
		}
	}
	for _, v := range pendingBuckets(s.entries, from, until) {
		include := len(dimensions) == 0
		for _, d := range dimensions {
			include = include || v.Identity.Dimension == d
		}
		if include {
			v.Identity = projectStoredIdentity(v.Identity)
			if err := consume(v); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolvePending identifies a changed immutable tail once. No database lock is held
// during attribution; concurrent snapshots serialize only this bounded cache fill.
func (t *historyTraffic) resolvePending(ctx context.Context, entries []pendingEntry) ([]pendingEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return entries, nil
	}
	for !t.pendingMu.TryLock() {
		if !cadence.Wait(ctx, time.Millisecond) {
			return nil, ctx.Err()
		}
	}
	defer t.pendingMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(entries) == len(t.pendingInput) && &entries[0] == &t.pendingInput[0] {
		return t.pendingResolved, nil
	}
	resolved := append([]pendingEntry(nil), entries...)
	latest := map[string]config.FlowFact{}
	for _, e := range entries {
		if e.fact != nil {
			latest[e.fact.Key] = *e.fact
		}
	}
	for key, f := range latest {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		f = cloneFlowFact(f)
		t.apps.identifyContext(ctx, &f)
		if t.route != nil {
			t.route(&f)
		}
		latest[key] = f
	}
	for i, e := range resolved {
		if e.fact != nil {
			f := trafficIdentity(*e.fact, latest[e.fact.Key])
			resolved[i].fact = &f
		}
	}
	resolved = pendingAttributed(resolved)
	t.pendingInput, t.pendingResolved = entries, resolved
	return resolved, nil
}

// searchMatches reads the same complete-bucket/exact-cut horizon as eachBucket.
func (s *trafficSnapshot) searchMatches(ctx context.Context, from, until time.Time, dimensions []string, direction string, search *trafficSearch) error {
	if search.term == "" {
		return nil
	}
	visit := func(key historystore.TrafficKey) error {
		if direction != "" && direction != "all" && key.Dimension != "dns" && key.Direction != direction {
			return nil
		}
		search.matched[searchKey(projectStoredIdentity(aggregateIdentity{Dimension: key.Dimension, Object: key.Object, Route: key.Route, Direction: key.Direction}))] = true
		return nil
	}
	boundary := until.Truncate(trafficBucket5m)
	if err := s.read.MatchingSearch(ctx, 0, from.Truncate(trafficBucket5m), boundary, dimensions, search.term, visit); err != nil {
		return err
	}
	if until.Before(time.Now().Add(-10 * time.Minute)) {
		return s.read.MatchingSearch(ctx, 0, boundary, boundary.Add(trafficBucket5m), dimensions, search.term, visit)
	}
	return s.read.MatchingSearch(ctx, 1, boundary, until, dimensions, search.term, visit)
}
