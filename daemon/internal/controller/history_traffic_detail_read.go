package controller

// Opened traffic details own a frozen read, independent of list/database retention.
// The cache's existing TTL, token/projection limits and shared-model budget still apply.
const trafficDetailReadKind = "traffic-detail"

func (r *historyRead) logModelRevoked() bool {
	if r.kind == trafficDetailReadKind || r.logVersion == nil {
		return false
	}
	v := r.logVersion
	if !v.valid.Load() {
		return true
	}
	if v.removedBefore != nil {
		before := v.removedBefore.Load()
		if before > v.meta.LogRemovalBeforeMs {
			return r.kind != "logs" || r.from.UnixMilli() < before
		}
	}
	return false
}

func (cache *readTokenCache) freezeTrafficDetail(r *historyRead) (*historyRead, string, error) {
	frozen := *r // All fact collections and model versions are immutable after publication.
	frozen.kind = trafficDetailReadKind
	token, err := cache.save(&frozen)
	return &frozen, token, err
}
