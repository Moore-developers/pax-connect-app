package controller

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"sort"
)

func (c *Controller) exitPoolProto() *paxv1.ExitPoolState {
	if c.exits == nil {
		return nil
	}
	s := c.exits.Snapshot()
	return &paxv1.ExitPoolState{ConfiguredLimit: uint32(s.ConfiguredLimit), InFlight: uint32(s.InFlight), Queued: uint32(s.Queued), Retrying: uint32(s.Retrying), AvailableProviders: uint32(s.AvailableProviders), TotalProviders: uint32(s.TotalProviders), At: timestamppb.New(s.At)}
}
func (c *Controller) reportRouteLatency(owner *testOwner, routeID, operation string, revision uint64, network string, value config.RouteMeasurement) {
	if owner.lifetime.Err() != nil {
		return
	}
	owner.mu.Lock()
	if owner.active[routeID] != operation {
		owner.mu.Unlock()
		return
	}
	owner.progress[routeID] = &paxv1.RouteMeasurementProgress{RouteId: routeID, OperationId: operation, TestedConfigRevision: revision, Network: network, Phase: paxv1.RouteMeasurementPhase_ROUTE_MEASUREMENT_PHASE_LATENCY, UpdatedAt: timestamppb.Now(), LatencyResult: &paxv1.RouteTestSummary{At: timestamppb.Now(), Success: true, DurationMs: uint32(value.Duration.Milliseconds()), TestedConfigRevision: revision, ColdMs: uint32(value.Cold.Milliseconds()), StableMs: uint32(value.Stable.Milliseconds()), StableSamples: value.StableSamples, StableError: value.StableError, Method: value.Method, TestTarget: value.TestTarget}}
	owner.mu.Unlock()
	c.scheduleMeasurementPublish()
}
func (c *Controller) scheduleMeasurementPublish() {
	if c.proxy == nil || c.proxy.lifetime.Err() != nil {
		return
	}
	select {
	case c.proxy.measurementSignal <- struct{}{}:
	default:
	}

}
func (c *Controller) measurementProgress() []*paxv1.RouteMeasurementProgress {
	if c.tests == nil {
		return nil
	}
	c.tests.mu.Lock()
	defer c.tests.mu.Unlock()
	var items []*paxv1.RouteMeasurementProgress
	for id, p := range c.tests.progress {
		if c.tests.active[id] != p.OperationId {
			continue
		}
		node, ok := c.routes.catalog.Find(id)
		if !ok {
			if cancel := c.tests.ops[p.OperationId]; cancel != nil {
				cancel()
			}
			continue
		}
		resolved, err := c.resolvedRecord(c.tests.lifetime, node)
		if err != nil || connectionVersion(resolved, resolved.Config.TLSResolved) != p.TestedConfigRevision {
			if cancel := c.tests.ops[p.OperationId]; cancel != nil {
				cancel()
			}
			continue
		}
		items = append(items, proto.Clone(p).(*paxv1.RouteMeasurementProgress))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].RouteId < items[j].RouteId })
	return items
}

func (c *Controller) beginRouteProgress(owner *testOwner, id, op string, revision uint64, network string) {
	owner.mu.Lock()
	owner.progress[id] = &paxv1.RouteMeasurementProgress{RouteId: id, OperationId: op, TestedConfigRevision: revision, Network: network, Phase: paxv1.RouteMeasurementPhase_ROUTE_MEASUREMENT_PHASE_LATENCY, UpdatedAt: timestamppb.Now()}
	owner.mu.Unlock()
	c.scheduleMeasurementPublish()
}
