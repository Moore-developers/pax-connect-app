package controller

import (
	"context"
	pb "pax-connect/daemon/gen/proto/paxv1"
)

func (c *Controller) historyRouteUsage(ctx context.Context, q *pb.RouteHistoryRequest) (*pb.RouteUsageResponse, error) {
	return c.trafficRouteUsage(ctx, q)
}
