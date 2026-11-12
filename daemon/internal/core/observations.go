package core

import (
	"context"
	"encoding/json"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "pax-connect/daemon/gen/proto/corev1"
	"pax-connect/daemon/internal/config"
)

func (s *server) ReadObservations(ctx context.Context, q *pb.ObservationRequest) (*pb.ObservationBatch, error) {
	runtime, ok := s.runtime.(interface {
		ReadObservationPage(context.Context, config.ObservationRead) config.ObservationBatch
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "observations_unavailable")
	}
	b := runtime.ReadObservationPage(ctx, config.ObservationRead{Session: q.Session, After: q.AfterSequence, Limit: int(q.Limit), ResetToken: q.ResetToken, Cursor: q.Cursor})
	out := &pb.ObservationBatch{Session: b.Session, HighWater: b.HighWater, LostBefore: b.LostBefore, Collecting: b.Collecting, ResetRequired: b.ResetRequired}
	out.ResetToken = b.ResetToken
	out.NextCursor = b.NextCursor
	out.BaselineComplete = b.BaselineComplete
	out.BaselineHighWater = b.BaselineHighWater
	for _, f := range b.Baseline {
		raw, e := json.Marshal(f)
		if e != nil {
			return nil, status.Error(codes.Internal, "observation_invalid")
		}
		out.Baseline = append(out.Baseline, raw)
	}
	for _, e := range b.Records {
		raw, err := json.Marshal(e)
		if err != nil {
			return nil, status.Error(codes.Internal, "observation_invalid")
		}
		out.Records = append(out.Records, raw)
	}
	return out, nil
}
