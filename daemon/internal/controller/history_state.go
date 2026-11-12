package controller

import (
	"encoding/json"
	"errors"
	"time"
)

var errHistory = errors.New("history_store_failed")

type historyMetadata struct {
	SummaryGeneration                                string
	LogClearedAt                                     time.Time
	LogRemovalBeforeMs                               int64
	LogsRevision, TrafficRevision, LogHeadSequence   uint64
	LogDeletionGeneration, TrafficDeletionGeneration uint64
	RouteHistoryRevision                             uint64
	JournalGeneration                                string
	Revision, ClearGeneration                        uint64
	Retention                                        int
	RetentionHours                                   int
	SettingsRevision                                 uint64
	Coverage                                         time.Time
	Partial, CapacityEvicted                         bool
	Collection, Error                                string
}

func historyTimeKey(at time.Time, id string) []byte {
	return []byte(at.UTC().Format("20060102150405.000000000") + "|" + id)
}
func decodeJSON(raw []byte, v any) error { return json.Unmarshal(raw, v) }
