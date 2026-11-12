package controller

import (
	"context"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/network"
)

type pathDiagnosticsOwner struct {
	mu         sync.Mutex
	inspect    func(context.Context, string) network.PathReport
	progressMu sync.Mutex
	progress   *pb.NetworkDiagnosticProgress
}

// ConfigurePathDiagnostics injects OS inspection at composition, outside the
// serialized product owner. Slow observation must never block Stop or imports.
func (c *Controller) ConfigurePathDiagnostics(inspect func(context.Context, string) network.PathReport) {
	c.pathDiagnostics = &pathDiagnosticsOwner{inspect: inspect}
}

type pathBoundary struct {
	runtimeEpoch        uint64
	iface, route, epoch string
	revision            uint64
	status              pb.ProtectionStatus
	epochKnown          bool
}

func (c *Controller) pathBoundary(ctx context.Context) (pathBoundary, config.TrafficPathEvidence, bool, error) {
	var boundary pathBoundary
	var evidence config.TrafficPathEvidence
	available := false
	err := c.query(ctx, func(context.Context) {
		boundary.runtimeEpoch = c.proxy.runtimeEpoch

		if c.proxy.networkEpoch != nil {
			var err error
			boundary.epoch, err = c.proxy.networkEpoch()
			boundary.epochKnown = err == nil
		}

		c.mu.Lock()
		defer c.mu.Unlock()
		boundary.route = c.current.GetCurrentRoute().GetId()
		boundary.revision = c.current.GetCurrentRoute().GetConfigRevision()
		boundary.status = c.current.Status
	})
	return boundary, evidence, available, err
}

func (c *Controller) GetTrafficPathDiagnostics(ctx context.Context, _ *emptypb.Empty) (*pb.TrafficPathDiagnostics, error) {
	p := c.pathDiagnostics
	if p == nil || p.inspect == nil {
		return nil, status.Error(codes.Unimplemented, "path_diagnostics_unavailable")
	}
	if !p.mu.TryLock() {
		return nil, status.Error(codes.ResourceExhausted, "path_diagnostics_busy")
	}
	defer p.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	before, _, _, err := c.pathBoundary(ctx)
	if err != nil {
		return nil, err
	}
	report := p.inspect(ctx, before.iface)
	if ctx.Err() != nil {
		return nil, status.FromContextError(ctx.Err()).Err()
	}

	after, evidence, available, err := c.pathBoundary(ctx)
	if err != nil {
		return nil, err
	}
	result := &pb.TrafficPathDiagnostics{At: timestamppb.Now(), Partial: report.Partial || !before.epochKnown || !after.epochKnown, ChangedDuringCapture: before != after || report.Changed, RuntimeObservationAvailable: available}
	for _, f := range report.Findings {
		result.Findings = append(result.Findings, &pb.TrafficPathFinding{Kind: f.Kind, Subject: f.Subject, State: f.State, Value: f.Value})
	}

	if !result.ChangedDuringCapture {
		result.TunConnections = evidence.TUNConnections
		result.MixedConnections = evidence.MixedConnections
		if !evidence.LastMixedConnection.IsZero() {
			result.LastMixedConnection = timestamppb.New(evidence.LastMixedConnection)
		}
		result.ProxyRoutes = evidence.ProxyRoutes
		result.DirectRoutes = evidence.DirectRoutes
		if !evidence.LastTUNConnection.IsZero() {
			result.LastTunConnection = timestamppb.New(evidence.LastTUNConnection)
		}
	}

	return result, nil
}
