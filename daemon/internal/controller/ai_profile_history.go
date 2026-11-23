package controller

import (
	"context"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/aiprofile"
)

// ListAIProfileHistory reads committed, credential-free client history.
func (s *AIProfilesServer) ListAIProfileHistory(ctx context.Context, r *pb.AIProfileHistoryQuery) (*pb.AIProfileHistoryPage, error) {
	before := aiprofile.HistoryCursor{At: r.Before.GetAtNs(), RowID: r.Before.GetRowId()}
	events, next, err := s.manager.ListClientHistory(ctx, aiprofile.CLI(r.Cli), before)
	if err != nil {
		return nil, aiError(err)
	}
	out := &pb.AIProfileHistoryPage{}
	for _, event := range events {
		out.Events = append(out.Events, &pb.AIProfileHistoryEvent{
			AtMs: event.At / 1e6, Kind: event.Kind, ProfileId: event.ProfileID,
			ProfileName: event.ProfileName, ServiceSource: event.ServiceSource,
		})
	}
	if next.RowID != 0 {
		out.Next = &pb.AIProfileHistoryCursor{AtNs: next.At, RowId: next.RowID}
	}
	return out, nil
}
