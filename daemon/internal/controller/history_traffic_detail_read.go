package controller

// Opened traffic details own a frozen read, independent of list/database retention.
// The cache's existing TTL, token/projection limits and snapshot budget still apply.
const trafficDetailReadKind = "traffic-detail"

func (r *historyRead) snapshotRevoked() bool {
	if r.columnSnapshot != nil && !r.columnSnapshot.Valid() {
		return true
	}
	if r.kind == "traffic" && r.columnSnapshot != nil && r.columnSnapshot.Removed() {
		return true
	}
	if r.kind == "logs" && r.columnSnapshot != nil && !r.columnSnapshot.ListValid(r.from.UnixMilli()) {
		return true
	}
	return false
}

func (cache *readTokenCache) freezeTrafficDetail(r *historyRead) (*historyRead, string, error) {
	frozen := *r // All fact collections and snapshots are immutable after publication.
	frozen.kind = trafficDetailReadKind
	token, err := cache.save(&frozen)
	return &frozen, token, err
}
