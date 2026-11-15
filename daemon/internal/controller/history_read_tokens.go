package controller

import (
	"errors"
	"slices"
	"sync"
	"time"

	"pax-connect/daemon/internal/historystore"
)

// readTokenCache holds every frozen read token, log and traffic. It lives outside the
// legacy owner goroutine, has its own mutex, and keeps the legacy GLOBAL budget of at most
// eight tokens and 32MiB (history-storage §6); log and traffic kinds share it.
type readTokenCache struct {
	mu                    sync.Mutex
	reads                 map[string]*historyRead
	columnSnapshots       map[*historystore.ColumnSnapshot]bool
	logDeletionGeneration uint64
	logRemovalBeforeMs    int64
	logSummaryGeneration  string
	logJournalGeneration  string
	now                   func() time.Time // token age clock; nil means time.Now (tests inject)
}

func (cache *readTokenCache) age(r *historyRead) time.Duration {
	created := r.createdAt
	if created.IsZero() {
		created = r.at
	}
	if cache.now != nil {
		return cache.now().Sub(created)
	}
	return time.Since(created)
}

func newReadTokenCache() *readTokenCache {
	return &readTokenCache{reads: map[string]*historyRead{}, columnSnapshots: map[*historystore.ColumnSnapshot]bool{}}
}

// read returns a live token of kind with the same query signature; an idle or aged token,
// or one with another signature, is dropped.
func (cache *readTokenCache) read(token, kind, signature string) (*historyRead, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	defer cache.releaseColumnSnapshotsLocked()
	r := cache.reads[token]
	if r == nil || r.snapshotRevoked() || r.kind != kind || r.signature != signature || time.Since(r.timeUsed) > 2*time.Minute || cache.age(r) > 5*time.Minute {
		delete(cache.reads, token)
		return nil, errors.New("history_cursor_expired")
	}
	r.timeUsed = time.Now()
	return r, nil
}

// take returns a token of kind without checking a query signature; detail lookups use it.
func (cache *readTokenCache) take(token string, kinds ...string) (*historyRead, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	defer cache.releaseColumnSnapshotsLocked()
	r := cache.reads[token]
	if r == nil || r.snapshotRevoked() || !slices.Contains(kinds, r.kind) || time.Since(r.timeUsed) > 2*time.Minute || cache.age(r) > 5*time.Minute {
		delete(cache.reads, token)
		return nil, errors.New("history_cursor_expired")
	}
	r.timeUsed = time.Now()
	return r, nil
}

// save stores one frozen page under a new token. Log pages carry the generations they were
// read at; a page built while the store was cleared or rebuilt is discarded.
func (cache *readTokenCache) save(r *historyRead) (string, error) {
	cache.mu.Lock()
	if r.columnSnapshot != nil {
		if cache.columnSnapshots == nil {
			cache.columnSnapshots = map[*historystore.ColumnSnapshot]bool{}
		}
		cache.columnSnapshots[r.columnSnapshot] = true
	}
	defer cache.mu.Unlock()
	defer cache.releaseColumnSnapshotsLocked()
	if r.snapshotRevoked() {
		return "", errors.New("history_cursor_expired")
	}
	if r.kind != trafficDetailReadKind && (r.kind == "logs" || r.logEvidence) {
		// Deletion generations only grow. A page read at a newer generation than the cache
		// has seen was read after a commit the cache has not been told about yet: adopt it.
		// An older page was read before a deletion and is discarded.
		if r.deletionGeneration > cache.logDeletionGeneration {
			cache.logDeletionGeneration, cache.logSummaryGeneration = r.deletionGeneration, r.summaryGeneration
			if r.logEvidence {
				cache.logJournalGeneration = r.logJournalGeneration
			}
			cache.dropLogEvidenceLocked()
		}
		safePrefix := r.kind == "logs" && cache.logRemovalBeforeMs > 0 && r.from.UnixMilli() >= cache.logRemovalBeforeMs
		if r.deletionGeneration != cache.logDeletionGeneration && !safePrefix || r.summaryGeneration != cache.logSummaryGeneration || r.logEvidence && r.logJournalGeneration != cache.logJournalGeneration {
			return "", errors.New("history_cursor_expired")
		}
	}
	total := r.bytes
	now := time.Now()
	for key, v := range cache.reads {
		if now.Sub(v.timeUsed) > 2*time.Minute || cache.age(v) > 5*time.Minute {
			delete(cache.reads, key)
		} else {
			total += v.bytes
		}
	}
	for len(cache.reads) >= 8 || total > 32<<20 {
		var oldest string
		var at time.Time
		for k, v := range cache.reads {
			if oldest == "" || v.timeUsed.Before(at) {
				oldest, at = k, v.timeUsed
			}
		}
		if oldest == "" {
			return "", errors.New("history_query_busy")
		}
		total -= cache.reads[oldest].bytes
		delete(cache.reads, oldest)
	}
	token := historyToken()
	if token == "" {
		return "", errors.New("history_unavailable")
	}
	r.timeUsed, r.createdAt = now, now
	cache.reads[token] = r
	return token, nil
}

// observeLogGenerations invalidates log tokens when logs.db reports new deletion or summary
// generations. Traffic tokens containing retained log evidence expire with them.
func (cache *readTokenCache) observeLogGenerations(deletion uint64, summary, journal string, removal ...int64) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	defer cache.releaseColumnSnapshotsLocked()
	nextJournal := journal
	if deletion == cache.logDeletionGeneration && summary == cache.logSummaryGeneration && nextJournal == cache.logJournalGeneration {
		return
	}
	before := int64(0)
	if len(removal) > 0 {
		before = removal[0]
	}
	scoped := before > 0 && summary == cache.logSummaryGeneration && journal == cache.logJournalGeneration
	cache.logDeletionGeneration, cache.logSummaryGeneration = deletion, summary
	cache.logJournalGeneration = nextJournal
	cache.logRemovalBeforeMs = before
	if !scoped {
		cache.dropLogEvidenceLocked()
		return
	}
	cache.dropLogEvidenceBeforeLocked(before)
}

// invalidate drops every token of one kind, as a committed traffic deletion did before.
func (cache *readTokenCache) invalidate(kind string) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	defer cache.releaseColumnSnapshotsLocked()
	cache.dropKindLocked(kind)
}

func (cache *readTokenCache) dropKindLocked(kind string) {
	for token, r := range cache.reads {
		if r.kind == kind {
			delete(cache.reads, token)
		}
	}
}
