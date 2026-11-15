package controller

import (
	"context"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

func columnTrafficEvidence(ctx context.Context, source columnProjectionSource, s config.LogConnectionSummary) (historystore.ColumnTrafficEvidence, error) {
	f := s.Fact
	kind := config.ClassifyTrafficSource(f)
	ms, known := config.FlowLatency(f)
	e := historystore.ColumnTrafficEvidence{Fact: f, SourceKind: kind.Kind, SourceID: kind.ID, SourceName: kind.Name, Latency: ms, LatencyKnown: known, Download: s.Metrics.Download}
	if kind.Kind == "dns" {
		flow := flowProto(f)
		e.GroupID = dnsTrafficGroupID(flow)
		future := f
		future.Failure = ""
		e.FutureGroupID = dnsTrafficGroupID(flowProto(future))
	}
	if f.Direction == "unknown" && f.RouteID == "" && f.Failure == "rule_rejected" && s.LastEventID != "" {
		last, ok, err := source.Event(ctx, s.LastEventID)
		if err != nil {
			return e, err
		}
		if ok && last.Flow != nil && last.Flow.Key == f.Key && last.Flow.Direction == "blocked" && last.Flow.Failure == "rule_rejected" && !last.At.IsZero() {
			e.CorrectionAt = last.At
			e.Corrected = *last.Flow
		}
	}
	return e, nil
}

// advanceColumnTrafficIndex fills one bounded batch; the log owner resumes it
// between ordinary collection commits, with no startup all-facts load.
func advanceColumnTrafficIndex(ctx context.Context, c *historystore.Columns) (bool, error) {
	ready, err := c.TrafficProjectionReady(ctx)
	if err != nil || ready {
		return ready, err
	}
	after, err := c.TrafficProjectionProgress(ctx)
	if err != nil {
		return false, err
	}
	batch, err := c.ConnectionsPage(ctx, after, 128)
	if err != nil {
		return false, err
	}
	if len(batch) > 0 {
		after = batch[len(batch)-1].Fact.Key
	}
	err = c.Update(ctx, func(tx *historystore.ColumnsTx) error {
		for _, s := range batch {
			e, err := columnTrafficEvidence(ctx, tx, s)
			if err != nil {
				return err
			}
			if err = tx.PutTrafficEvidence(ctx, e); err != nil {
				return err
			}
		}
		return tx.SetTrafficProjectionProgress(ctx, after, len(batch) < 128)
	})
	return len(batch) < 128, err
}
