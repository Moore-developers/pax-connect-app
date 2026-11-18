package controller

import (
	"cmp"
	"context"
	"slices"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

const routingProcessLimit = 512

// QueryRoutingProcesses freezes the application and process rankings for one rule editor.
func (c *Controller) QueryRoutingProcesses(ctx context.Context, _ *emptypb.Empty) (*pb.RoutingProcessCandidates, error) {
	store, err := c.logStore(ctx)
	if err != nil {
		return nil, routingProcessQueryError(ctx)
	}
	reader, ok := store.(interface {
		RoutingCandidateTraffic(context.Context, int64, int64) ([]historystore.RoutingCandidateBytes, error)
	})
	if !ok {
		return nil, routingProcessQueryError(ctx)
	}
	meta, err := store.SnapshotMeta(ctx)
	if err != nil {
		return nil, routingProcessQueryError(ctx)
	}
	now := c.now()
	from := now.Add(-time.Duration(normalizedRetentionHours(meta.RetentionHours)) * time.Hour).UnixMilli()
	values, err := reader.RoutingCandidateTraffic(ctx, max(from, meta.LogRemovalBeforeMs), now.UnixMilli())
	if err != nil {
		return nil, routingProcessQueryError(ctx)
	}
	apps, err := c.historyUserApplications(ctx)
	if err != nil {
		return nil, routingProcessQueryError(ctx)
	}
	processTraffic, appTraffic := routingCandidateTotals{}, routingCandidateTotals{}
	for _, row := range values {
		if app, ok := userApplicationMatch(row.ApplicationID, row.Process, apps); ok {
			appTraffic.add(app.ID, row)
		}
		if _, err := config.NormalizeRoutingTarget("process", row.Process); err == nil {
			processTraffic.add(row.Process, row)
		}
	}
	applications := appTraffic.ranked(len(appTraffic))
	processes := processTraffic.ranked(routingProcessLimit)
	paths := make([]string, len(processes))
	for i, process := range processes {
		paths[i] = process.Id
	}
	return &pb.RoutingProcessCandidates{
		Paths: paths, Limited: len(processTraffic) > len(processes),
		Applications: applications, Processes: processes,
	}, nil
}

type routingCandidateTotals map[string]historystore.RoutingCandidateBytes

func (totals routingCandidateTotals) add(id string, row historystore.RoutingCandidateBytes) {
	total := totals[id]
	total.Bytes += row.Bytes
	total.LastActivityMs = max(total.LastActivityMs, row.LastActivityMs)
	totals[id] = total
}

func (totals routingCandidateTotals) ranked(limit int) []*pb.RoutingCandidateTraffic {
	ranked := make([]*pb.RoutingCandidateTraffic, 0, len(totals))
	for id, total := range totals {
		ranked = append(ranked, &pb.RoutingCandidateTraffic{Id: id, Bytes: total.Bytes})
	}
	slices.SortFunc(ranked, func(a, b *pb.RoutingCandidateTraffic) int {
		return cmp.Or(cmp.Compare(b.Bytes, a.Bytes),
			cmp.Compare(totals[b.Id].LastActivityMs, totals[a.Id].LastActivityMs),
			cmp.Compare(a.Id, b.Id))
	})
	return ranked[:min(len(ranked), limit)]
}

func routingProcessQueryError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return status.FromContextError(err).Err()
	}
	return status.Error(codes.Unavailable, "routing_processes_unavailable")
}
