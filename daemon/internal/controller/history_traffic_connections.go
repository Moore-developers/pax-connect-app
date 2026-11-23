package controller

import (
	"context"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/historystore"
	"pax-connect/daemon/internal/stats"
)

// freezeTrafficConnections reads connection summaries from logs.db. Byte totals are the
// recorded summary, never inferred from the current settings or long-term aggregate.
func (c *Controller) freezeTrafficConnections(ctx context.Context, r *historyRead, key string) error {
	job, err := c.logJob()
	if err != nil {
		return err
	}
	snapshot, columnErr := job.logColumnSnapshot(ctx)
	if columnErr != nil {
		return columnErr
	}
	r.columnSnapshot = snapshot
	applyTrafficLogMeta(r, snapshot.Meta())
	if key != "" {
		f := historystore.ColumnTrafficFilter{ConnectionKey: key, From: r.from, Until: r.at, DetailAt: r.detailAt}
		page, err := snapshot.TrafficConnections(ctx, f, 0, 1)
		if err != nil {
			return err
		}
		for _, v := range page.Items {
			r.aggregateFlows = append(r.aggregateFlows, stats.FlowWindow{Fact: v.Fact, Upload: v.Metrics.Upload, Download: v.Metrics.Download})
		}
	}
	return nil
}
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// connectionTrafficRead serves the log page's connection-key link directly, without a
// traffic scan and without relying on a traffic row still containing that connection.
func (c *Controller) connectionTrafficRead(ctx context.Context, key string) (*historyRead, string, error) {
	at := c.now()
	r := &historyRead{at: at, from: at.Add(-24 * time.Hour), kind: "traffic", signature: querySignature([]string{"connection", key}), dimension: "application", meta: c.publishedTrafficMeta(), flows: map[string][]*pb.FlowRecord{}}
	if err := c.freezeTrafficConnections(ctx, r, key); err != nil {
		return nil, "", err
	}
	return c.finishTrafficRead(ctx, r, &pb.TrafficQuery{Dimension: "application", Window: "day"}, r.aggregateFlows)
}

func applyTrafficLogMeta(r *historyRead, meta historystore.LogsMeta) {
	r.logEvidence = true
	r.deletionGeneration, r.summaryGeneration = meta.LogDeletionGeneration, meta.SummaryGeneration
	r.logJournalGeneration = meta.JournalGeneration
	r.recentResponsesState = "ready"
	if meta.Partial {
		r.recentResponsesState = "partial"
	}
	r.meta.LogsRevision, r.meta.LogDeletionGeneration = meta.LogsRevision, meta.LogDeletionGeneration
	r.meta.ClearGeneration = meta.ClearGeneration
	r.meta.SummaryGeneration, r.meta.JournalGeneration = meta.SummaryGeneration, meta.JournalGeneration
}
