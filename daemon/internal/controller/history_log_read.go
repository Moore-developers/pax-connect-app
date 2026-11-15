package controller

import (
	"context"
	"errors"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// logReads is the read-only logs.db surface. It is defined here, in the consumer, so the
// query layer only sees the statements it actually needs.
type logReads interface {
	RecentEvents(ctx context.Context, limit int) ([]config.HistoryEvent, error)
	LogDiagnostics(ctx context.Context, flowKey string, fromMs, untilMs, head int64) ([]historystore.LogDiagnostic, error)
	SnapshotMeta(ctx context.Context) (historystore.LogsMeta, error)
}

// logJob returns the running log writer, or history_unavailable before configuration.
func (c *Controller) logJob() (*historyLogs, error) {
	c.logsMu.Lock()
	l := c.logs
	c.logsMu.Unlock()
	if l == nil {
		return nil, errors.New("history_unavailable")
	}
	return l, nil
}

// logStore returns the log database through the controller's configured writer.
func (c *Controller) logStore(ctx context.Context) (logReads, error) {
	c.logsMu.Lock()
	l := c.logs
	c.logsMu.Unlock()
	if l == nil {
		return nil, errors.New("history_unavailable")
	}
	if ready, ok := l.logs.(interface{ Ready(context.Context) error }); ok {
		if err := ready.Ready(ctx); err != nil {
			return nil, err
		}
	}
	return l.logs, nil
}

// publishedTrafficMeta returns the legacy owner's committed metadata. Log queries take
// every log-related field from logs.db instead (R3) and add these traffic fields.
func (c *Controller) publishedTrafficMeta() historyMetadata {
	if t := c.trafficJob(); t != nil {
		meta := historyMetadata{}
		if stored := t.meta.Load(); stored != nil {
			meta = *stored
		}
		if live := c.liveView.Load(); live != nil {
			if live.Snapshot(time.Now()).Known {
				meta.Collection = "collecting"
			} else {
				meta.Collection = "idle"
			}
		}
		return meta
	}

	c.mu.Lock()
	h := c.current.GetHistory()
	c.mu.Unlock()
	var meta historyMetadata
	if h == nil {
		return meta
	}
	meta.Revision = h.Revision
	meta.Coverage = h.CoverageStart.AsTime()
	meta.Collection = h.CollectionState
	meta.TrafficRevision = h.TrafficRevision
	meta.TrafficDeletionGeneration = h.TrafficDeletionGeneration
	meta.RouteHistoryRevision = h.RouteHistoryRevision
	return meta
}

// logsHistoryMeta composes one log response state from logs.db meta plus the traffic fields
// of the published legacy state. Field mapping matches the old historyState() log section.
func (c *Controller) logsHistoryMeta(m historystore.LogsMeta) historyMetadata {
	meta := c.publishedTrafficMeta()
	meta.RetentionHours = normalizedRetentionHours(m.RetentionHours)
	meta.Retention = int(logRetentionDays(meta.RetentionHours))
	meta.SettingsRevision = m.SettingsRevision
	meta.LogsRevision = m.LogsRevision
	meta.LogDeletionGeneration = m.LogDeletionGeneration
	meta.LogRemovalBeforeMs = m.LogRemovalBeforeMs
	meta.SummaryGeneration = m.SummaryGeneration
	meta.JournalGeneration = m.JournalGeneration
	meta.LogHeadSequence = m.HeadSequence
	meta.ClearGeneration = m.ClearGeneration
	meta.LogClearedAt = m.ClearedAt
	meta.Partial = m.Partial
	meta.CapacityEvicted = m.CapacityEvicted
	meta.Error = m.ErrorCode
	return meta
}

// validateLogRead keeps the legacy filter contract of the raw log query.
func validateLogRead(q *pb.LogQuery) error {
	if !validLogView(q.View) || (!validDimension(q.Dimension) && q.Dimension != "system") || len(q.Search) > 200 || q.Level != "" && q.Level != "info" && q.Level != "warning" && q.Level != "error" {
		return errors.New("invalid_filter")
	}
	return validateLogCriteria(q)
}

// QueryLogs reads a frozen page of user-facing events; raw diagnostics are separate.
func (c *Controller) QueryLogs(ctx context.Context, q *pb.LogQuery) (*pb.LogPage, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := c.logStore(ctx); err != nil {
		return nil, historyError(err)
	}
	out, err := c.queryColumnLogPage(ctx, q)
	if err != nil {
		return nil, historyError(err)
	}
	if err := c.readTokens.ensureLive(ctx, out.ReadToken); err != nil {
		return nil, historyError(err)
	}
	return out, nil
}

// QueryLogObjects indexes the entire saved event selection, independently of severity.
func (c *Controller) QueryLogObjects(ctx context.Context, q *pb.LogQuery) (*pb.LogObjectPage, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := c.logStore(ctx); err != nil {
		return nil, historyError(err)
	}
	out, err := c.queryColumnLogObjects(ctx, q)
	if err != nil {
		return nil, historyError(err)
	}
	if err := c.readTokens.ensureLive(ctx, out.ReadToken); err != nil {
		return nil, historyError(err)
	}
	return out, nil
}

// Connection charts and pages carry private retained log facts too.
func (cache *readTokenCache) dropLogEvidenceLocked() { cache.dropLogEvidenceBeforeLocked(0) }

func (cache *readTokenCache) dropLogEvidenceBeforeLocked(before int64) {
	for token, r := range cache.reads {
		if r.kind != trafficDetailReadKind && (r.kind == "logs" || r.logEvidence) &&
			(before <= 0 || r.kind != "logs" || r.from.UnixMilli() < before) {
			delete(cache.reads, token)
		}
	}
}
