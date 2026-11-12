package controller

import (
	"context"
	"encoding/json"
	"errors"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
	"pax-connect/daemon/internal/stats"
	"sort"
	"time"
)

// freezeTrafficConnections reads connection summaries from logs.db. Byte totals are the
// recorded summary, never inferred from the current settings or long-term aggregate.
func (c *Controller) freezeTrafficConnections(ctx context.Context, r *historyRead, key string) error {
	job, err := c.logJob()
	if err != nil {
		return err
	}
	if v, e := job.readModel(ctx); v != nil || e != nil {
		if e != nil {
			return e
		}
		r.logVersion = v
		detailAt := r.detailAt
		if detailAt.IsZero() {
			detailAt = r.at
		}
		if !r.aggregate {
			for _, entry := range v.ordered {
				f := entry.summary.Fact
				if key != "" {
					if f.Key != key {
						continue
					}
				} else if f.StartedAt.Before(detailAt.Add(-24*time.Hour)) || f.StartedAt.After(detailAt) {
					continue
				}
				r.aggregateFlows = append(r.aggregateFlows, v.flow(entry, detailAt))
				r.bytes += 8
			}
		}
		applyTrafficLogMeta(r, v.meta)
		return nil
	}
	source, ok := job.logs.(interface {
		ConnectionSnapshot(context.Context, int64, int64, string, func(historystore.LogConnection) error) (historystore.LogsMeta, error)
	})
	if !ok {
		return errors.New("history_unavailable")
	}
	bytesBefore := r.bytes
	detailAt := r.detailAt
	if detailAt.IsZero() {
		detailAt = r.at
	}
	cutoff := detailAt.Add(-24 * time.Hour)
	meta, err := source.ConnectionSnapshot(ctx, cutoff.UnixMilli(), detailAt.UnixMilli(), key, func(row historystore.LogConnection) error {
		var summary config.LogConnectionSummary
		if e := json.Unmarshal(row.Body, &summary); e != nil {
			return e
		}
		f := summary.Fact
		// Older folds lost an explicit empty-route rejection to the preceding
		// unknown identity. Correct only the read copy, using its retained event.
		if f.Direction == "unknown" && f.RouteID == "" && f.Failure == "rule_rejected" && len(row.LastEventBody) > 0 {
			var event config.HistoryEvent
			if e := json.Unmarshal(row.LastEventBody, &event); e != nil {
				return e
			}
			if rejected := event.Flow; event.ID == summary.LastEventID && rejected != nil && rejected.Key == f.Key &&
				rejected.Direction == "blocked" && rejected.Failure == "rule_rejected" && !event.At.IsZero() && !event.At.After(detailAt) {
				f.Direction, f.RouteID, f.RouteName = rejected.Direction, rejected.RouteID, rejected.RouteName
				f.Rule, f.RuleSource = rejected.Rule, rejected.RuleSource
			}
		}
		if f.ObservedUntil != nil && f.ObservedUntil.After(detailAt) || f.ClosedAt != nil && f.ClosedAt.After(detailAt) {
			f.ClosedAt, f.ObservedUntil = nil, nil
			f.ResponseKnown, f.StateUnknown = false, true
			f.EstablishmentState, f.Failure, f.Phase = "", "", ""
		}
		r.aggregateFlows = append(r.aggregateFlows, stats.FlowWindow{Fact: f, Upload: summary.Metrics.Upload, Download: summary.Metrics.Download})
		r.bytes += len(row.Body) + 256
		if r.bytes > 24<<20 {
			return errors.New("history_query_busy")
		}
		return nil
	})
	if err != nil {
		r.aggregateFlows = nil
		r.bytes = bytesBefore
		r.recentResponsesState, r.recentResponsesError = "failed", "history_store_failed"
		return err
	}
	applyTrafficLogMeta(r, meta)
	sort.Slice(r.aggregateFlows, func(i, j int) bool {
		a, b := r.aggregateFlows[i].Fact, r.aggregateFlows[j].Fact
		if a.StartedAt.Equal(b.StartedAt) {
			return a.Key < b.Key
		}
		return a.StartedAt.After(b.StartedAt)
	})
	return err
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
	at := time.Now()
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
	if meta.Partial || meta.CapacityEvicted {
		r.recentResponsesState = "partial"
	}
	r.meta.LogsRevision, r.meta.LogDeletionGeneration = meta.LogsRevision, meta.LogDeletionGeneration
	r.meta.SummaryGeneration, r.meta.JournalGeneration = meta.SummaryGeneration, meta.JournalGeneration
}
