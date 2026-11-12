package controller

import (
	"context"
	"errors"
	"testing"

	"pax-connect/daemon/internal/aiusage"
)

func TestAIUsageLogsRejectGenerationChangedAfterRead(t *testing.T) {
	state := aiusage.State{Revision: 3, Generation: 1}
	calls := 0
	rows, after, err := readAIUsageLogs(context.Background(), func(context.Context, aiusage.Query) (aiusage.Logs, error) {
		calls++
		// The SQLite page has finished; deletion/reprice commits before the response's State.
		old := aiusage.Logs{All: 7}
		state.Revision++
		state.Generation++
		return old, nil
	}, func() aiusage.State { return state }, aiusage.Query{})
	if !errors.Is(err, aiusage.ErrBusy) || rows.All != 0 || len(rows.Requests) != 0 || after.Generation != 2 || calls != 1 {
		t.Fatalf("stale content must be discarded without a retry: rows=%+v state=%+v err=%v calls=%d", rows, after, err, calls)
	}
}

func TestAIUsageLogsOrdinaryAppendRetainsOnePageSnapshot(t *testing.T) {
	state := aiusage.State{Revision: 3, Generation: 1}
	rows, after, err := readAIUsageLogs(context.Background(), func(context.Context, aiusage.Query) (aiusage.Logs, error) {
		state.Revision++
		return aiusage.Logs{All: 7}, nil
	}, func() aiusage.State { return state }, aiusage.Query{})
	if err != nil || rows.All != 7 || after.Revision != 4 {
		t.Fatalf("ordinary revision must not mix page counters or fail: rows=%+v state=%+v err=%v", rows, after, err)
	}
}
