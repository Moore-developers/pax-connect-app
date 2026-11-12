package core

import (
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "pax-connect/daemon/gen/proto/corev1"
	"pax-connect/daemon/internal/config"
)

// VerifyCurrentPath holds the lifecycle mutex only for preparation and final proof.
func (s *server) VerifyCurrentPath(ctx context.Context, q *pb.CurrentPathRequest) (*pb.CurrentPathResult, error) {
	if q.CheckId == "" || len(q.CheckId) > 128 || len(q.ExpectedTag) > 128 {
		return nil, status.Error(codes.InvalidArgument, "current_path_invalid")
	}
	s.mu.Lock()
	runtime, ok := s.runtime.(interface {
		PrepareCurrentPath(context.Context, config.CurrentPathRequest) (func(context.Context) config.CurrentPathResult, error)
		CurrentPathIdentity() (string, uint64)
	})
	if !ok || s.stopping {
		s.mu.Unlock()
		return nil, status.Error(codes.Unavailable, "current_path_unavailable")
	}
	run, err := runtime.PrepareCurrentPath(ctx, config.CurrentPathRequest{CheckID: q.CheckId, ExpectedTag: q.ExpectedTag, ExpectedSession: q.ExpectedSession, ExpectedVersion: q.ExpectedVersion, ConfigRevision: q.ConfigRevision})
	s.mu.Unlock()
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	result := run(ctx)
	s.mu.Lock()
	session, version := runtime.CurrentPathIdentity()
	if session != result.CoreSession || version != result.RuntimeVersion || s.stopping {
		result.State = "stale"
		result.Cause = "runtime_changed"
	}
	s.mu.Unlock()
	return &pb.CurrentPathResult{CheckId: result.CheckID, State: result.State, Cause: result.Cause, ActualTag: result.ActualTag, CoreSession: result.CoreSession, RuntimeVersion: result.RuntimeVersion, StartedUnixMilli: result.StartedAt.UnixMilli(), CompletedUnixMilli: result.CompletedAt.UnixMilli()}, nil
}
