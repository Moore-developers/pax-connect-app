package controller

import "pax-connect/daemon/internal/historystore"

// Shared independent details may reference the same frozen column revision.
// Release it only after the last cache token has gone, including failed saves.
func (cache *readTokenCache) releaseColumnSnapshotsLocked() {
	live := map[*historystore.ColumnSnapshot]bool{}
	for _, r := range cache.reads {
		if r.columnSnapshot != nil {
			live[r.columnSnapshot] = true
		}
	}
	for s := range cache.columnSnapshots {
		if !live[s] {
			_ = s.Close()
			delete(cache.columnSnapshots, s)
		}
	}
}
