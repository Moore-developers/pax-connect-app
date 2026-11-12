package controller

import (
	"context"
	"errors"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
	"time"
)

// GetLogDetail returns persisted, scrubbed evidence, never fixture-derived fields.
func (c *Controller) GetLogDetail(ctx context.Context, q *pb.LogDetailRequest) (*pb.LogDetail, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	source, err := c.logStore()
	if err != nil {
		return nil, historyError(err)
	}
	var rawVersion *logModelVersion
	if q.RecordId == "" {
		job, err := c.logJob()
		if err != nil {
			return nil, historyError(err)
		}
		rawVersion, err = job.readModel(ctx)
		if err != nil {
			return nil, historyError(err)
		}
	}
	var out *pb.LogDetail
	if q.RecordId != "" {
		out, err = c.logViewDetail(ctx, source, q)
	} else {
		out, err = c.logEventDetail(ctx, source, q)
	}
	if err != nil {
		return nil, historyError(err)
	}
	if q.Query.GetReadToken() != "" {
		if err := c.readTokens.ensureLive(ctx, q.Query.ReadToken); err != nil {
			return nil, historyError(err)
		}
	}
	if rawVersion != nil {
		if err := rawVersion.ensureValid(ctx); err != nil {
			return nil, historyError(err)
		}
	}
	return out, nil
}

// logEventDetail answers the stable event identity path: raw text plus one flow, if any.
func (c *Controller) logEventDetail(ctx context.Context, source logReads, q *pb.LogDetailRequest) (*pb.LogDetail, error) {
	meta, err := source.SnapshotMeta(ctx)
	if err != nil {
		return nil, err
	}
	row, ok, err := source.ReadLogEvent(ctx, q.EventId)
	if err != nil {
		return nil, err
	}
	var event config.HistoryEvent
	if ok {
		event, err = logQueryEvent(row)
		if err != nil {
			return nil, err
		}
	}
	if !ok || event.ID == "" || event.At.Before(time.Now().Add(-time.Duration(meta.RetentionHours)*time.Hour)) {
		return nil, errors.New("history_expired")
	}
	out := &pb.LogDetail{Event: eventProto(event), Raw: event.Raw}
	// The identity path keeps the NEWEST eight raw lines overall, including diagnostics;
	// the grouped detail path keeps the first eight. Both follow the legacy contract.
	if event.Flow != nil {
		diagnostics, err := source.LogDiagnostics(ctx, event.Flow.Key, historystore.FullLogRangeMin, historystore.FullLogRangeMax, historystore.FullLogRangeMax)
		if err != nil {
			return nil, err
		}
		for _, raw := range diagnostics {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			out.Raw = append(out.Raw, raw.Text)
			if len(out.Raw) > 8 {
				out.Raw = out.Raw[len(out.Raw)-8:]
				out.Truncated = true
			}
		}
	}
	out.Truncated = event.Truncated
	out.RawOmitted = event.RawOmitted
	if event.Flow != nil {
		out.Flow = flowProto(*event.Flow)
	}
	return out, nil
}
