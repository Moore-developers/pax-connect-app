package controller

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
)

type networkDiagnoser interface {
	DiagnoseNetwork(context.Context, config.RouteConfig, string) (measurement.NetworkDiagnosticReport, config.RouteMeasurement, error)
}

// DiagnoseNetwork is an explicit 45-second read-only query; RPC cancellation owns
// every probe. It shares native admission and bandwidth exclusion with route tests.
func (c *Controller) DiagnoseNetwork(ctx context.Context, req *pb.NetworkDiagnosticRequest) (*pb.NetworkDiagnosticReport, error) {
	if !validDiagnosticOperation(req.OperationId) || (req.ReadProgress && req.OperationId == "") {
		return nil, status.Error(codes.InvalidArgument, "diagnostic_operation_invalid")
	}
	if req.ReadProgress {
		if c.pathDiagnostics == nil {
			return nil, status.Error(codes.Unimplemented, "network_diagnostics_unavailable")
		}
		return c.pathDiagnostics.readProgress(req.OperationId)
	}
	if req.Mode != "quick" && req.Mode != "services" && req.Mode != "full" && req.Mode != "sources" {
		return nil, status.Error(codes.InvalidArgument, "network_diagnostic_mode_invalid")
	}
	tester, ok := c.tester.(networkDiagnoser)
	if !ok || c.tests == nil || c.pathDiagnostics == nil {
		return nil, status.Error(codes.Unimplemented, "network_diagnostics_unavailable")
	}
	if !c.pathDiagnostics.mu.TryLock() {
		return nil, status.Error(codes.ResourceExhausted, "path_diagnostics_busy")
	}
	defer c.pathDiagnostics.mu.Unlock()
	ctx = measurement.WithDiagnosticObserver(ctx, c.pathDiagnostics.beginProgress(req.OperationId))
	defer c.pathDiagnostics.finishProgress()
	capture, err := c.captureConnectionDiagnosis(ctx)
	if err != nil {
		return nil, err
	}
	if capture.report.Steps[0].State != "pass" {
		return nil, status.Error(codes.FailedPrecondition, capture.report.Steps[0].Code)
	}
	if c.physical == nil {
		return nil, status.Error(codes.FailedPrecondition, "physical_network_unavailable")
	}
	bounded, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	physical, err := c.physical.sample(bounded)
	if err != nil || physical.Epoch == "" || physical.Name == "" {
		return nil, status.Error(codes.FailedPrecondition, "physical_network_unavailable")
	}
	contextFacts, contextToken, contextErr := c.captureNetworkContext(bounded)
	if contextErr != nil {
		return nil, contextErr
	}
	owner := c.tests
	owner.mu.Lock()
	if owner.closed {
		owner.mu.Unlock()
		return nil, status.Error(codes.Unavailable, "daemon_stopping")
	}
	if owner.bandwidth != "" {
		owner.mu.Unlock()
		return nil, status.Error(codes.ResourceExhausted, "download_busy")
	}
	if owner.activeLatency > 0 {
		owner.mu.Unlock()
		return nil, status.Error(codes.ResourceExhausted, "measurement_busy")
	}
	select {
	case <-owner.slots:
	default:
		owner.mu.Unlock()
		return nil, status.Error(codes.ResourceExhausted, "measurement_busy")
	}
	owner.bandwidth = "network_diagnostics"
	owner.probes.Add(1)
	owner.mu.Unlock()
	defer func() {
		owner.mu.Lock()
		owner.bandwidth = ""
		owner.mu.Unlock()
		owner.slots <- struct{}{}
		owner.probes.Done()
	}()
	stop := context.AfterFunc(owner.lifetime, cancel)
	defer stop()
	profile := bindRouteNetwork(capture.profile, physical)
	profile.MeasurementNetwork = physical.Epoch
	value, exit, err := tester.DiagnoseNetwork(bounded, profile, req.Mode)
	if ctx.Err() != nil {
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	result := networkDiagnosticProto(value)
	result.Facts = append(result.Facts, contextFacts...)
	result.TargetRouteId = capture.report.TargetRouteId
	result.TargetRouteName = capture.report.TargetRouteName
	result.ConfigRevision = capture.report.TargetRevision
	result.RunningRouteId = capture.report.RunningRouteId
	result.RunningRouteName = capture.report.RunningRouteName
	result.Network = physical.Epoch
	result.Mode = req.Mode
	result.At = timestamppb.Now()
	result.ExitError = exit.ExitError
	if exit.ExitLocation != nil {
		result.Exit = exitLocationProto(exit.ExitLocation)
	}
	if err != nil && len(result.Probes) == 0 && result.ProviderError == "" {
		result.ProviderError = measurement.Classify(err, false).Code
	}
	// Do not use the expired measurement budget for final identity validation.
	check, done := context.WithTimeout(ctx, time.Second)
	defer done()
	after, captureErr := c.captureConnectionDiagnosis(check)
	latest, physicalErr := c.physical.sample(check)
	_, afterToken, contextErr := c.captureNetworkContext(check)
	result.ChangedDuringCapture = captureErr != nil || physicalErr != nil || latest.Epoch != physical.Epoch || contextErr != nil || contextToken != afterToken
	if captureErr == nil {
		result.ChangedDuringCapture = result.ChangedDuringCapture || capture.boundary != after.boundary || result.TargetRouteId != after.report.TargetRouteId || result.ConfigRevision != after.report.TargetRevision || capture.report.OperationId != after.report.OperationId
	}
	return result, nil
}

func networkDiagnosticProto(v measurement.NetworkDiagnosticReport) *pb.NetworkDiagnosticReport {
	r := &pb.NetworkDiagnosticReport{ScoreModel: v.ScoreModel, ScoreCeiling: int32(v.ScoreCeiling), IpType: v.IPType, Timezone: v.Timezone, Country: v.Country, ProviderError: v.ProviderError, IntelAnswered: uint32(v.IntelAnswered), IntelTotal: uint32(v.IntelTotal)}
	if v.Score != nil {
		score := int32(*v.Score)
		r.Score = &score
	}
	for _, s := range v.Sources {
		r.Sources = append(r.Sources, &pb.NetworkDiagnosticSource{Name: s.Name, Country: s.Country, City: s.City, Asn: s.ASN, Org: s.Org, ProviderDate: s.ProviderDate, ProviderId: s.ProviderID})
	}
	for _, f := range v.Facts {
		r.Facts = append(r.Facts, &pb.NetworkDiagnosticFact{Group: f.Group, Label: f.Label, Value: f.Value, Source: f.Source, State: f.State, ProviderId: f.ProviderID})
	}
	for _, p := range v.Providers {
		r.Providers = append(r.Providers, &pb.NetworkDiagnosticProvider{Id: p.ID, QueryIp: p.QueryIP, Family: p.Family, State: p.State, ErrorCode: p.ErrorCode, Capabilities: p.Capabilities, HttpStatus: uint32(p.HTTPStatus), DurationMs: uint64(max(0, p.Duration.Milliseconds())), At: timestamppb.New(p.At), FallbackFor: p.FallbackFor, FallbackReason: p.FallbackReason})
	}
	for _, p := range v.Probes {
		r.Probes = append(r.Probes, &pb.NetworkDiagnosticProbe{Target: p.Target, Attempt: uint32(p.Attempt), HttpStatus: uint32(p.HTTPStatus), DurationMs: uint64(max(0, p.Duration.Milliseconds())), ErrorCode: p.ErrorCode, ExpectedResponse: p.ExpectedResponse, At: timestamppb.New(p.At), State: p.State, Stage: p.Stage, RedirectHost: p.RedirectHost})
	}
	return r
}
