package coreclient

import (
	"context"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	pb "pax-connect/daemon/gen/proto/corev1"
	"pax-connect/daemon/internal/config"
	"time"
)

// VerifyCurrentPath reads only immutable launch identity; no lifecycle session fields.
func (s *Session) VerifyCurrentPath(ctx context.Context, q config.CurrentPathRequest) (config.CurrentPathResult, error) {
	client, closer, _, err := s.open(ctx)
	if err != nil {
		return config.CurrentPathResult{}, err
	}
	defer func() { _ = closer.Close() }()
	hello, err := client.Hello(ctx, &emptypb.Empty{})
	if err != nil {
		return config.CurrentPathResult{}, ErrUnavailable
	}
	if hello.SelectedTag != q.ExpectedTag {
		return config.CurrentPathResult{CheckID: q.CheckID, State: "failed", Cause: "runtime_selection_mismatch", CoreSession: hello.RuntimeSession, RuntimeVersion: hello.RuntimeVersion}, nil
	}
	if q.ExpectedSession != "" && q.ExpectedSession != hello.RuntimeSession || q.ExpectedVersion != 0 && q.ExpectedVersion != hello.RuntimeVersion {
		return config.CurrentPathResult{CheckID: q.CheckID, State: "stale", Cause: "current_path_stale", CoreSession: hello.RuntimeSession, RuntimeVersion: hello.RuntimeVersion}, nil
	}
	if q.ExpectedSession == "" {
		q.ExpectedSession = hello.RuntimeSession
	}
	if q.ExpectedVersion == 0 {
		q.ExpectedVersion = hello.RuntimeVersion
	}
	v, err := client.VerifyCurrentPath(ctx, &pb.CurrentPathRequest{CheckId: q.CheckID, ExpectedTag: q.ExpectedTag, ExpectedSession: q.ExpectedSession, ExpectedVersion: q.ExpectedVersion, ConfigRevision: q.ConfigRevision})
	if err != nil {
		cause := status.Convert(err).Message()
		var state string
		switch cause {
		case "mixed_listener_unreachable", "runtime_selection_mismatch":
			state = "failed"
		case "current_path_stale":
			state = "stale"
		default:
			return config.CurrentPathResult{}, ErrUnavailable
		}
		return config.CurrentPathResult{CheckID: q.CheckID, State: state, Cause: cause, CoreSession: hello.RuntimeSession, RuntimeVersion: hello.RuntimeVersion}, nil
	}
	return config.CurrentPathResult{CheckID: v.CheckId, State: v.State, Cause: v.Cause, ActualTag: v.ActualTag, CoreSession: v.CoreSession, RuntimeVersion: v.RuntimeVersion, StartedAt: time.UnixMilli(v.StartedUnixMilli), CompletedAt: time.UnixMilli(v.CompletedUnixMilli)}, nil
}
