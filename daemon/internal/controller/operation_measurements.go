package controller

import (
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"time"
)

// Pending outcomes belong to the existing serial product owner, not saved route
// snapshots: cancellation, edits and deletion must not erase this batch's facts.
func (c *Controller) beginMeasurementOutcome(id string, items []*pb.RouteMeasurementResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.measurementOutcomes == nil {
		c.measurementOutcomes = map[string]*pb.RouteMeasurementsResult{}
	}
	c.measurementOutcomes[id] = &pb.RouteMeasurementsResult{Items: items}
}
func (c *Controller) recordMeasurementOutcome(owner *testOwner, id string, item *pb.RouteMeasurementResult) {
	owner.mu.Lock()
	if parent := owner.parents[id]; parent != "" {
		id = parent
	}
	owner.mu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if result := c.measurementOutcomes[id]; result != nil {
		for i, previous := range result.Items {
			if previous.RouteId == item.RouteId {
				result.Items[i] = item
				return
			}
		}
	}
}
func routeTestSummary(result routeTestResult) *pb.RouteTestSummary {
	if result.at.IsZero() {
		result.at = time.Now()
	}
	summary := &pb.RouteTestSummary{
		At:       timestamppb.New(result.at),
		StableMs: uint32(result.measurement.Stable.Milliseconds()), StableSamples: result.measurement.StableSamples, StableError: result.measurement.StableError, TestTarget: result.measurement.TestTarget,
		ExitIp:       result.exitIP,
		ExitLocation: exitLocationProto(result.measurement.ExitLocation), LastExitLocation: exitLocationProto(result.measurement.LastExitLocation),
		ColdMs: uint32(result.measurement.Cold.Milliseconds()), SecondMs: uint32(result.measurement.Second.Milliseconds()), Reused: result.measurement.Reused, Method: result.measurement.Method, FirstError: result.measurement.FirstError, SecondError: result.measurement.SecondError,
		Success:              result.success,
		DurationMs:           uint32(result.duration.Milliseconds()),
		TestedRevision:       result.revision,
		TestedConfigRevision: result.configRevision,
	}
	if result.exitError != "" {
		summary.ExitError = &pb.ProductError{Code: result.exitError}
	}
	if result.code != "" {
		summary.Error = &pb.ProductError{Code: result.code, MessageParams: result.params}
	}
	return summary
}
func (c *Controller) recordLatencyOutcome(owner *testOwner, id, route string, result routeTestResult, err error) {
	if err != nil {
		result.code, result.params = catalogFailure(err)
		result.success = false
	}
	summary := routeTestSummary(result)
	c.recordMeasurementOutcome(owner, id, &pb.RouteMeasurementResult{RouteId: route, ConfigRevision: result.configRevision, Success: result.success, Error: summary.Error, Measurement: &pb.RouteMeasurementResult_Latency{Latency: summary}})
}
