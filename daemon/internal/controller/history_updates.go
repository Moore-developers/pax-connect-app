package controller

import (
	"context"
	"errors"
	"pax-connect/daemon/internal/historystore"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "pax-connect/daemon/gen/proto/paxv1"
)

// QueryLogUpdates scans only newly committed event identities, never historical pages or raw text.
func (c *Controller) QueryLogUpdates(ctx context.Context, q *pb.LogUpdateRequest) (*pb.LogUpdatePage, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err := c.logStore(ctx)
	if err != nil {
		return nil, historyError(err)
	}
	job, _ := c.logJob()
	columnSnapshot, err := job.logColumnSnapshot(ctx)
	if err != nil {
		return nil, historyError(err)
	}
	defer func() { _ = columnSnapshot.Close() }()
	meta := columnSnapshot.Meta()
	out := &pb.LogUpdatePage{Exact: true, AsOf: timestamppb.Now(), History: historyState(c.logsHistoryMeta(meta)), HighWater: meta.HeadSequence, NextSequence: q.AfterSequence}
	if q.JournalGeneration != meta.JournalGeneration || q.DeletionGeneration != meta.LogDeletionGeneration || q.AfterSequence > meta.HeadSequence {
		out.ResetRequired = true
		return out, nil
	}
	filter := q.GetFilter()
	if filter == nil {
		filter = &pb.LogQuery{}
	}
	if validateLogRead(filter) != nil || filter.Search != "" || len(filter.ObjectId) > 4096 {
		return nil, historyError(errors.New("invalid_filter"))
	}
	limit := min(200, max(1, int(q.Limit)))
	if q.Limit == 0 {
		limit = 200
	}
	deadline := time.Now().Add(100 * time.Millisecond)
	cutoff := time.Now().Add(-time.Duration(meta.RetentionHours) * time.Hour)
	rows, err := columnSnapshot.EventsAfter(ctx, int64(q.AfterSequence), int64(meta.HeadSequence), 2001)
	if err != nil {
		return nil, historyError(err)
	}
	touched := map[string]bool{}
	bytes := 0
	scan := func(load func(context.Context, string) (*pb.ProductLog, error)) error {
		for i, event := range rows {
			if i >= 2000 || time.Now().After(deadline) || !q.CountOnly && len(out.Items) >= limit || bytes >= 1<<20 {
				out.HasMore, out.Exact = true, false
				break
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			n := event.Sequence
			out.NextSequence = n
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
			groupCtx, cancel := context.WithDeadline(ctx, deadline)
			item, e := load(groupCtx, key)
			cancel()
			if errors.Is(e, context.DeadlineExceeded) && ctx.Err() == nil {
				out.HasMore, out.Exact = true, false
				out.NextSequence = n - 1
				break
			}
			if e != nil {
				return e
			}
			if item != nil {
				out.Count++
				if !q.CountOnly {
					out.Items = append(out.Items, item)
					bytes += proto.Size(item)
				} else {
					out.Exact = false
				}
			}
		}
		return nil
	}
	if filter.View == "" {
		err = scan(func(ctx context.Context, key string) (*pb.ProductLog, error) {
			return c.columnLogUpdateGroup(ctx, columnSnapshot, filter, key, cutoff)
		})
	} else {
		apps, e := c.historyUserApplications(ctx)
		if e != nil {
			return nil, historyError(e)
		}
		from, until := cutoff, time.Now()
		if filter.From != nil {
			from = filter.From.AsTime()
		}
		if filter.Until != nil {
			until = filter.Until.AsTime()
		}
		r := &historyRead{columnSnapshot: columnSnapshot, columnLogRead: &columnLogReadState{window: historystore.ColumnLogWindow{View: filter.View, Route: filter.RouteId, From: from, Until: until, Cutoff: cutoff, Apps: apps}}}
		err = r.withColumnLogs(ctx, filter, func(selection *historystore.ColumnLogSelection) error {
			return scan(func(ctx context.Context, key string) (*pb.ProductLog, error) {
				matches, e := selection.ContainsKey(ctx, columnLogFilter(filter), key)
				if e != nil || !matches {
					return nil, e
				}
				return reduceLogUpdateGroup(ctx, selection, filter, key)
			})
		})
	}
	if err != nil {
		return nil, historyError(err)
	}

	if !out.HasMore {
		out.NextSequence = out.HighWater
	}
	from := cutoff
	if filter.From != nil && filter.From.AsTime().After(from) {
		from = filter.From.AsTime()
	}
	if !columnSnapshot.ListValid(from.UnixMilli()) {
		return nil, historyError(errors.New("history_cursor_expired"))
	}
	return out, nil
}
