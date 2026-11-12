package controller

import (
	"context"
	"reflect"
	"testing"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// TestBaselineResetBelowCommittedSequenceIsSkipped pins the legacy watermark rule: a
// baseline reset whose high water is below the committed sequence must not roll it back or
// admit its records. The journal path reproduces it through the ingest plan, and the stored
// log state stays byte-for-byte equal to the legacy accept path.
func TestBaselineResetBelowCommittedSequenceIsSkipped(t *testing.T) {
	ctx := context.Background()
	h := newLogsHarness(t)
	h.l.route = New("test", "test").mapHistoryRoute
	flowA := testLogFlow("s/1/a")
	flowB := testLogFlow("s/1/b")
	first := config.ObservationBatch{Session: "s", HighWater: 3, Collecting: true, Records: []config.Observation{
		logObservation(1, "started", cloneLogFlow(flowA), 0, 0, "", ""),
		logObservation(2, "sample", cloneLogFlow(flowA), 5, 50, "", ""),
		logObservation(3, "closed", cloneLogFlow(flowA), 0, 0, "", ""),
	}}
	in := newHistoryIngest(h.journal, nil)
	in.seeding = false
	if err := in.commit(ctx, in.plan(first)); err != nil {
		t.Fatal(err)
	}
	h.settle()
	// A stale baseline arrives with the same session and a high water below the committed
	// sequence; its baseline flow and record must both be ignored.
	reset := config.ObservationBatch{Session: "s", HighWater: 3, ResetToken: "restart", BaselineHighWater: 2, BaselineComplete: true, Collecting: true,
		Baseline: []config.FlowFact{*cloneLogFlow(flowB)},
		Records:  []config.Observation{logObservation(2, "sample", cloneLogFlow(flowB), 1, 1, "", "")}}
	if err := in.commit(ctx, in.plan(reset)); err != nil {
		t.Fatal(err)
	}
	h.settle()
	cursor, ok, err := h.journal.Cursor(ctx)
	if err != nil || !ok || cursor != (historystore.Cursor{Session: "s", Seq: 3}) {
		t.Fatalf("reset moved the journal cursor: %+v %v %v", cursor, ok, err)
	}
	oldSummaries, oldEvents, oldDiagnostics := readGoldenLogState(t, "reset")
	newSummaries, newEvents, newDiagnostics := readLogState(t, h.logs)
	if !reflect.DeepEqual(oldSummaries, newSummaries) {
		t.Fatalf("summaries differ:\nold %s\nnew %s", pretty(oldSummaries), pretty(newSummaries))
	}
	if !reflect.DeepEqual(oldEvents, newEvents) {
		t.Fatalf("events differ:\nold %s\nnew %s", pretty(oldEvents), pretty(newEvents))
	}
	if !reflect.DeepEqual(oldDiagnostics, newDiagnostics) {
		t.Fatalf("diagnostics differ:\nold %s\nnew %s", pretty(oldDiagnostics), pretty(newDiagnostics))
	}
	if _, leaked := newSummaries[flowB.Key]; leaked {
		t.Fatal("stale baseline flow was admitted")
	}
	for _, event := range newEvents {
		if event.Flow != nil && event.Flow.Key == flowB.Key {
			t.Fatal("stale baseline record was admitted")
		}
	}
}
