package controller

import (
	"context"
	"errors"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

func (c *Controller) columnLogDetail(ctx context.Context, source logReads, q *pb.LogDetailRequest) (*pb.LogDetail, error) {
	if q.RecordId == "" {
		job, err := c.logJob()
		if err != nil {
			return nil, err
		}
		snapshot, err := job.logColumnSnapshot(ctx)
		if err != nil {
			return nil, err
		}
		defer func() { _ = snapshot.Close() }()
		e, ok, err := snapshot.Event(ctx, q.EventId)
		if err != nil {
			return nil, err
		}
		if !ok || e.ID == "" || e.At.Before(time.Now().Add(-time.Duration(snapshot.Meta().RetentionHours)*time.Hour)) {
			return nil, errors.New("history_expired")
		}
		out := &pb.LogDetail{Event: eventProto(e), Raw: e.Raw}
		if e.Flow != nil {
			d, err := source.LogDiagnostics(ctx, e.Flow.Key, historystore.FullLogRangeMin, historystore.FullLogRangeMax, historystore.FullLogRangeMax)
			if err != nil {
				return nil, err
			}
			for _, v := range d {
				out.Raw = append(out.Raw, v.Text)
				if len(out.Raw) > 8 {
					out.Raw = out.Raw[len(out.Raw)-8:]
					out.Truncated = true
				}
			}
			out.Flow = flowProto(*e.Flow)
		}
		out.Truncated = e.Truncated
		out.RawOmitted = e.RawOmitted
		if snapshot.Removed() {
			return nil, errors.New("history_expired")
		}
		if !snapshot.Valid() {
			return nil, errors.New("history_cursor_expired")
		}
		return out, nil
	}
	filter := q.Query
	if filter == nil || filter.View == "" || filter.ReadToken == "" {
		return nil, errors.New("invalid_filter")
	}
	r, _, err := c.columnLogRead(ctx, filter)
	if err != nil {
		return nil, err
	}
	var out *pb.LogDetail
	err = r.withColumnLogs(ctx, filter, func(s *historystore.ColumnLogSelection) error {
		found, er := s.ContainsKey(ctx, columnLogFilter(filter), q.RecordId)
		if er != nil {
			return er
		}
		if !found {
			return errors.New("history_expired")
		}
		g, found, er := s.Group(ctx, q.RecordId)
		if er != nil {
			return er
		}
		if !found {
			return errors.New("history_expired")
		}
		e, found, er := s.Event(ctx, g.EventID, false)
		if er != nil {
			return er
		}
		if !found {
			return errors.New("history_expired")
		}
		out = &pb.LogDetail{TotalEvents: g.Count}
		if filter.View == "activity" {
			record := materializeColumnLogRecord(r, columnGroupRecord(g, e))
			out.Record = record
			out.Event = &pb.ProductLog{EventId: record.EventId, RecordId: record.RecordId, Flow: record.Flow}
			out.Flow = record.Flow
			if record.Flow != nil {
				out.Event.ConnectionKey = record.Flow.Key
			}
		} else {
			out.Event = columnGroupProduct(g, e)
			out.Flow = out.Event.Flow
		}
		return s.EachGroupEvent(ctx, q.RecordId, 2, func(event config.HistoryEvent) error {
			if filter.View == "activity" && len(out.Timeline) >= 200 {
				return errColumnTimelineDone
			}
			if len(out.Timeline) < 200 {
				out.Timeline = append(out.Timeline, eventProto(event))
			}
			if len(out.Raw) < 8 {
				saved, ok, e2 := s.Event(ctx, event.ID, true)
				if e2 != nil {
					return e2
				}
				if !ok {
					return errors.New("history_expired")
				}
				out.Raw = append(out.Raw, saved.Raw...)
				out.Raw = out.Raw[:min(8, len(out.Raw))]
				out.Truncated = out.Truncated || saved.Truncated
				out.RawOmitted = saved.RawOmitted
			}
			return nil
		})
	})
	if err != nil && !errors.Is(err, errColumnTimelineDone) {
		return nil, err
	}
	if out.Flow != nil {
		diagnostics, er := source.LogDiagnostics(ctx, out.Flow.Key, r.from.UnixMilli(), r.until.UnixMilli(), r.diagnosticsHead)
		if er != nil {
			return nil, er
		}
		for _, v := range diagnostics {
			if len(out.Raw) < 8 {
				out.Raw = append(out.Raw, v.Text)
			} else {
				out.Truncated = true
			}
		}
	}
	return out, nil
}

var errColumnTimelineDone = errors.New("column timeline complete")
