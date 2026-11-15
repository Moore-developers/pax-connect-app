package controller

import (
	"context"
	"sort"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// The owner already owns at most 128 freshly inserted events. If the indexed
// group count proves these are the complete group, reuse those exact facts
// rather than issuing several SQL reads for every row just inserted.
type columnBatchGroups struct {
	groups    map[string]map[string][]config.HistoryEvent
	summaries map[string]config.LogConnectionSummary
}

func newColumnBatchGroups(b historystore.ColumnImportBatch) *columnBatchGroups {
	v := &columnBatchGroups{groups: map[string]map[string][]config.HistoryEvent{}, summaries: map[string]config.LogConnectionSummary{}}
	for _, s := range b.Connections {
		v.summaries[s.Fact.Key] = s
	}
	for _, view := range []string{"activity", "access", "problems", "operations"} {
		v.groups[view] = map[string][]config.HistoryEvent{}
		for _, e := range b.Events {
			if key := logViewKey(e, view); key != "" {
				v.groups[view][key] = append(v.groups[view][key], e)
			}
		}
	}
	return v
}
func (v *columnBatchGroups) project(ctx context.Context, tx *historystore.ColumnsTx, view, key string) (historystore.ColumnGroup, error) {
	events := v.groups[view][key]
	if len(events) > 0 {
		count, err := tx.GroupEventCount(ctx, view, key)
		if err != nil {
			return historystore.ColumnGroup{}, err
		}
		if count == uint64(len(events)) {
			source := columnBatchStream{events: events, summaries: v.summaries, tx: tx}
			return projectColumnGroupStream(ctx, source, view, key, time.Time{}, "collecting")
		}
	}
	return projectColumnGroup(ctx, tx, view, key)
}

type columnBatchStream struct {
	events    []config.HistoryEvent
	summaries map[string]config.LogConnectionSummary
	tx        *historystore.ColumnsTx
}

type columnBatchEvidence struct {
	*historystore.ColumnsTx
	events map[string]config.HistoryEvent
}

func (s columnBatchEvidence) Event(ctx context.Context, id string) (config.HistoryEvent, bool, error) {
	if e, ok := s.events[id]; ok {
		return e, true, nil
	}
	return s.ColumnsTx.Event(ctx, id)
}

func (s columnBatchStream) GroupEventEach(ctx context.Context, _ string, _ string, order int, fn func(config.HistoryEvent) error) error {
	events := append([]config.HistoryEvent(nil), s.events...)
	sort.Slice(events, func(i, j int) bool {
		a, b := events[i], events[j]
		if a.At.Equal(b.At) {
			if order == 0 {
				return a.Sequence < b.Sequence
			}
			return a.ID < b.ID
		}
		return a.At.Before(b.At)
	})
	for _, e := range events {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}
func (s columnBatchStream) GroupRelated(ctx context.Context, _, _ string) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	keys := map[string]bool{}
	for _, e := range s.events {
		if e.Flow != nil {
			keys[e.Flow.Key] = true
		}
	}
	return uint64(len(keys)), nil
}
func (s columnBatchStream) Connection(ctx context.Context, key string) (config.LogConnectionSummary, bool, error) {
	if v, ok := s.summaries[key]; ok {
		return v, true, nil
	}
	return s.tx.Connection(ctx, key)
}
