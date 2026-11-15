package controller

import (
	"context"
	"math"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

func (c *Controller) columnLogUpdateGroup(ctx context.Context, snapshot *historystore.ColumnSnapshot, filter *pb.LogQuery, key string, cutoff time.Time) (*pb.ProductLog, error) {
	from, until := time.Unix(0, math.MinInt64), time.Unix(0, math.MaxInt64)
	if filter.From != nil {
		from = filter.From.AsTime()
	}
	if filter.Until != nil {
		until = filter.Until.AsTime()
	}
	var row *pb.ProductLog
	err := snapshot.WithRead(ctx, func(q historystore.ColumnQuery) error {
		s := historystore.ColumnLogStream(q, historystore.ColumnLogWindow{View: filter.View, Route: filter.RouteId, ConnectionKey: filter.ConnectionKey, From: from, Until: until, Cutoff: cutoff})
		var state logViewGroupState
		if err := s.EachGroupEvent(ctx, key, 0, func(e config.HistoryEvent) error { state.observe(e, filter.View); return nil }); err != nil {
			return err
		}
		if state.count == 0 {
			return nil
		}
		related, err := s.Related(ctx, key)
		if err != nil {
			return err
		}
		p := state.product(key, filter.View, related)
		if filter.Level != "" && state.event.Level != filter.Level {
			return nil
		}
		id, _ := logViewObject(state.event, filter.Dimension)
		if filter.ObjectId != "" && filter.ObjectId != "all" && id != filter.ObjectId {
			return nil
		}
		row = p
		return nil
	})
	return row, err
}
