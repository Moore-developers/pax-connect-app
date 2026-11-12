package controller

import (
	"context"
	"errors"
	"time"
)

// A conservative charge of unique payloads plus each retained version's directories.
// Entries shared by eight tokens count once; superseded payloads remain charged until eviction.
func modelVersionsBytes(versions map[*logModelVersion]bool) int {
	var current *logModelVersion
	for v := range versions {
		if current == nil || v.serial > current.serial {
			current = v
		}
	}
	if current == nil {
		return 0
	}
	total := current.bytes
	if len(versions) == 1 {
		return total
	}
	connections := map[*logModelConnection]bool{}
	events := map[*logModelEvent]bool{}
	groups := map[*logActivityGroup]bool{}
	for v := range versions {
		if v == current {
			continue
		}
		total += (len(v.connections)+len(v.events)+len(v.activity))*64 + len(v.ordered)*8
		for _, entries := range v.members {
			total += len(entries)*8 + 64
		}
		for _, entries := range v.candidates {
			total += len(entries)*8 + 64
		}
		for key, e := range v.connections {
			if current.connections[key] != e && !connections[e] {
				connections[e] = true
				total += e.bytes
			}
		}
		for key, e := range v.events {
			if current.events[key] != e && !events[e] {
				events[e] = true
				total += e.bytes
			}
		}
		for key, g := range v.activity {
			if current.activity[key] != g && !groups[g] {
				groups[g] = true
				total += g.bytes
			}
		}
	}
	return total
}

func (cache *readTokenCache) retainedVersionsLocked(current *logModelVersion) map[*logModelVersion]bool {
	versions := map[*logModelVersion]bool{current: true}
	for _, r := range cache.reads {
		if r.logVersion != nil {
			versions[r.logVersion] = true
		}
	}
	return versions
}
func modelRetainedBytes(v *logModelVersion, cache *readTokenCache) int {
	if cache == nil {
		return modelVersionsBytes(map[*logModelVersion]bool{v: true})
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return modelVersionsBytes(cache.retainedVersionsLocked(v))
}
func (cache *readTokenCache) makeLogModelRoom(v *logModelVersion) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for modelVersionsBytes(cache.retainedVersionsLocked(v)) > logModelBudget {
		oldest := ""
		var at time.Time
		for key, r := range cache.reads {
			if r.logVersion != nil && (oldest == "" || r.timeUsed.Before(at)) {
				oldest, at = key, r.timeUsed
			}
		}
		if oldest == "" {
			return
		}
		delete(cache.reads, oldest)
	}
}
func (cache *readTokenCache) ensureLive(ctx context.Context, token string) error {
	// Never wait on the store while holding the cache lock: the committing owner
	// needs that lock to publish revocation. Hold the read barrier through the final
	// cache check so a multi-transaction eviction cannot expose already-deleted facts.
	cache.mu.Lock()
	r := cache.reads[token]
	cache.mu.Unlock()
	if r == nil {
		return errors.New("history_cursor_expired")
	}
	var failure error
	check := func() {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		saved := cache.reads[token]
		if saved != r || r.logModelRevoked() {
			failure = errors.New("history_cursor_expired")
		}
	}
	if r.kind != trafficDetailReadKind && r.logVersion != nil && r.logVersion.readBarrier != nil {
		if err := r.logVersion.readBarrier(ctx, check); err != nil {
			return err
		}
	} else {
		if err := ctx.Err(); err != nil {
			return err
		}
		check()
	}
	return failure
}

func (cache *readTokenCache) setCurrentLogModel(v *logModelVersion) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.modelCurrent = v
}
func (cache *readTokenCache) modelFitsLocked(v *logModelVersion) bool {
	versions := cache.retainedVersionsLocked(v)
	if cache.modelCurrent != nil {
		versions[cache.modelCurrent] = true
	}
	return modelVersionsBytes(versions) <= logModelBudget
}

func (v *logModelVersion) ensureValid(ctx context.Context) error {
	var err error
	check := func() {
		if !v.valid.Load() || v.removedBefore != nil && v.removedBefore.Load() > v.meta.LogRemovalBeforeMs {
			err = errors.New("history_expired")
		}
	}
	if v.readBarrier != nil {
		if failure := v.readBarrier(ctx, check); failure != nil {
			return failure
		}
	} else {
		if failure := ctx.Err(); failure != nil {
			return failure
		}
		check()
	}
	return err
}
