package controller

import (
	"context"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
)

// GetLogDetail returns persisted, scrubbed evidence, never fixture-derived fields.
func (c *Controller) GetLogDetail(ctx context.Context, q *pb.LogDetailRequest) (*pb.LogDetail, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	source, err := c.logStore(ctx)
	if err != nil {
		return nil, historyError(err)
	}
	out, err := c.columnLogDetail(ctx, source, q)
	if err != nil {
		return nil, historyError(err)
	}
	if q.Query.GetReadToken() != "" {
		if err := c.readTokens.ensureLive(ctx, q.Query.ReadToken); err != nil {
			return nil, historyError(err)
		}
	}
	return out, nil
}
