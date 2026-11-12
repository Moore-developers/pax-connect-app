package controller

import (
	"context"

	"errors"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
	"time"
)

// QueryLogUpdates scans only newly committed event identities, never historical pages or raw text.
func (c *Controller) QueryLogUpdates(ctx context.Context, q *pb.LogUpdateRequest) (*pb.LogUpdatePage, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	source, err := c.logStore()
	if err != nil {
		return nil, historyError(err)
	}
	meta, err := source.SnapshotMeta(ctx)
	if err != nil {
		return nil, historyError(err)
	}
	out := &pb.LogUpdatePage{Exact: true, AsOf: timestamppb.Now(), History: historyState(c.logsHistoryMeta(meta)), HighWater: meta.HeadSequence, NextSequence: q.AfterSequence}
	if q.JournalGeneration != meta.JournalGeneration || q.DeletionGeneration != meta.LogDeletionGeneration || q.AfterSequence > meta.HeadSequence {
		out.ResetRequired = true
		return out, nil
	}
	filter := q.GetFilter()
	if filter == nil {
		filter = &pb.LogQuery{}
	}
	if !validLogView(filter.View) || !validDimension(filter.Dimension) || filter.Search != "" || len(filter.ObjectId) > 4096 || filter.Level != "" && filter.Level != "info" && filter.Level != "warning" && filter.Level != "error" {
		return nil, historyError(errors.New("invalid_filter"))
	}
	limit := min(200, max(1, int(q.Limit)))
	if q.Limit == 0 {
		limit = 200
	}
	deadline := time.Now().Add(100 * time.Millisecond)
	cutoff := time.Now().Add(-time.Duration(meta.RetentionHours) * time.Hour)
	rows, err := source.LogEventsAfter(ctx, int64(q.AfterSequence), int64(meta.HeadSequence), 2001)
	if err != nil {
		return nil, historyError(err)
	}
	touched := map[string]bool{}
	bytes := 0
	for i, row := range rows {
		if i >= 2000 || time.Now().After(deadline) || !q.CountOnly && len(out.Items) >= limit || bytes >= 1<<20 {
			out.HasMore, out.Exact = true, false
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, historyError(err)
		}
		n := uint64(row.Sequence)
		out.NextSequence = n
		event, err := logQueryEvent(row)
		if err != nil {
			return nil, historyError(err)
		}
		if event.At.Before(cutoff) {
			continue
		}
		if filter.From != nil && !event.At.After(filter.From.AsTime()) || filter.Until != nil && event.At.After(filter.Until.AsTime()) {
			continue
		}
		if filter.RouteId != "" && (event.Flow == nil || event.Flow.RouteID != filter.RouteId) || filter.View == "" && filter.Level != "" && event.Level != filter.Level {
			continue
		}
		object, _ := logObject(event, filter.Dimension)
		if filter.View != "" {
			object, _ = logViewObject(event, filter.Dimension)
		}
		if filter.ObjectId != "" && filter.ObjectId != "all" && filter.ObjectId != object {
			continue
		}
		if filter.View == "" {
			out.Count++
			if !q.CountOnly {
				item := eventProto(event)
				out.Items = append(out.Items, item)
				bytes += len(item.Message) + len(item.Domain) + len(item.Application) + 1024
			}
			continue
		}
		key := logViewKey(event, filter.View)
		if key == "" {
			continue
		}
		if filter.ConnectionKey != "" && (event.Flow == nil || event.Flow.Key != filter.ConnectionKey) {
			continue
		}
		if filter.ProblemId != "" && filter.View == "problems" && key != filter.ProblemId {
			continue
		}
		if touched[key] {
			continue
		}
		touched[key] = true
		members := []config.HistoryEvent{event}
		if filter.View != "operations" && (filter.View != "activity" || event.Flow != nil) {
			flowKey := ""
			if event.Flow != nil {
				flowKey = event.Flow.Key
			}
			members, err = logViewMembers(ctx, source, filter, key, flowKey, cutoff, deadline)
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				out.HasMore, out.Exact = true, false
				out.NextSequence = n - 1
				break
			}
			if err != nil {
				return nil, historyError(err)
			}
		}
		items, err := projectLogView(ctx, members, filter)
		if err != nil {
			return nil, historyError(err)
		}
		for _, item := range items {
			out.Count++
			if !q.CountOnly {
				out.Items = append(out.Items, item)
				bytes += proto.Size(item)
			} else {
				out.Exact = false
			}
		}
	}
	if !out.HasMore {
		out.NextSequence = out.HighWater
	}
	return out, nil
}

// logViewMembers reloads the frozen members of one connection or problem group, bounded by
// the same 24MiB and 100ms budget as the legacy index scan.
func logViewMembers(ctx context.Context, source logReads, q *pb.LogQuery, key, flowKey string, cutoff, deadline time.Time) ([]config.HistoryEvent, error) {
	fromMs, untilMs := int64(historystore.FullLogRangeMin), int64(historystore.FullLogRangeMax)
	if q.From != nil {
		fromMs = q.From.AsTime().UnixMilli()
	}
	if q.Until != nil {
		untilMs = q.Until.AsTime().UnixMilli()
	}
	var events []config.HistoryEvent
	size := 0
	visit := func(row historystore.LogEvent) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return context.DeadlineExceeded
		}
		size += len(row.Body)
		if size > 24<<20 {
			return errors.New("history_query_busy")
		}
		event, err := logQueryEvent(row)
		if err != nil {
			return err
		}
		if event.At.Before(cutoff) {
			return nil
		}
		if q.From != nil && !event.At.After(q.From.AsTime()) || q.Until != nil && event.At.After(q.Until.AsTime()) {
			return nil
		}
		if q.RouteId != "" && (event.Flow == nil || event.Flow.RouteID != q.RouteId) {
			return nil
		}
		event.Raw = nil
		events = append(events, event)
		return nil
	}
	var err error
	if q.View == "problems" {
		err = source.EachProblemLogEvent(ctx, key, fromMs, untilMs, cutoff.UnixMilli(), visit)
	} else {
		err = source.EachFlowLogEvent(ctx, flowKey, fromMs, untilMs, cutoff.UnixMilli(), visit)
	}
	return events, err
}
