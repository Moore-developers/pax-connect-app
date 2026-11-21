package controller

import (
	"context"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/environment"
)

// InspectEnvironment never captures a selected route or binds a physical NIC.
// The singleton short-lived session is read explicitly; its only polling is the bounded, read-only
// live-view lookup for STUN route evidence after the browser reports.
func (c *Controller) InspectEnvironment(ctx context.Context, req *pb.EnvironmentDiagnosticRequest) (*pb.EnvironmentDiagnosticReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if req.Action == "start" {
		var presentation *environment.Presentation
		if req.Presentation != nil {
			presentation = &environment.Presentation{Locale: req.Presentation.Locale, Messages: req.Presentation.Messages}
			if err := environment.ValidatePresentation(presentation); err != nil {
				return nil, status.Error(codes.InvalidArgument, "browser_presentation_invalid")
			}
		}
		admit := func() (*pb.OperationAck, error) { return c.startEnvironment(presentation) }
		var ack *pb.OperationAck
		var err error
		if req.Context != nil {
			ack, err = c.contextRequest(ctx, "environment.start", req, req.Context, admit)
		} else {
			ack, err = c.appRequest(ctx, "environment.start", req, "", admit)
		}
		if err != nil {
			return nil, err
		}
		c.environmentMu.Lock()
		defer c.environmentMu.Unlock()
		if c.environmentOperation != ack.OperationId || c.environmentSession == nil {
			return nil, status.Error(codes.NotFound, "environment_session_expired")
		}
		return c.environmentReportLocked(), nil
	}
	c.environmentMu.Lock()
	session := c.environmentSession
	if session == nil || session.Read().ID != req.SessionId {
		c.environmentMu.Unlock()
		return nil, status.Error(codes.NotFound, "environment_session_expired")
	}
	if req.Action == "sources" {
		if c.environmentSources != "" || !session.Read().Complete {
			err := c.environmentBusyLocked()
			c.environmentMu.Unlock()
			return nil, err
		}
		c.environmentSources = "app"
		c.environmentMu.Unlock()
		defer func() { c.environmentMu.Lock(); c.environmentSources = ""; c.environmentMu.Unlock() }()
		bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		return environmentReportProto(session.RetrySources(bounded)), nil
	}
	defer c.environmentMu.Unlock()
	switch req.Action {
	case "cancel":
		session.Close()
	case "read":
	default:
		return nil, status.Error(codes.InvalidArgument, "environment_action_invalid")
	}
	return c.environmentReportLocked(), nil
}

func (c *Controller) environmentReportLocked() *pb.EnvironmentDiagnosticReport {
	value := c.environmentSession.Read()
	result := environmentReportProto(value)
	result.OperationId = c.environmentOperation
	result.ExpiresAt = timestamppb.New(value.At.Add(45 * time.Second))
	return result
}
func (c *Controller) startEnvironment(presentation *environment.Presentation) (*pb.OperationAck, error) {
	c.environmentMu.Lock()
	defer c.environmentMu.Unlock()
	if c.tests == nil {
		return nil, status.Error(codes.Unavailable, "environment_unavailable")
	}
	if c.environmentSources != "" || c.environmentSession != nil && !c.environmentSession.Read().Complete {
		return nil, c.environmentBusyLocked()
	}
	session, err := environment.StartWithPresentation(c.tests.lifetime, c.exits, environmentEvidence{c}, presentation)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "environment_listener_failed")
	}
	ack, err := c.startOwnedProbe(func(task context.Context, id string) (*pb.OperationResult, error) {
		stop := context.AfterFunc(task, session.Close)
		defer stop()
		<-session.Done()
		value := session.Read()
		report := environmentReportProto(value)
		report.BrowserUrl = ""
		report.OperationId = id
		report.ExpiresAt = timestamppb.New(value.At.Add(45 * time.Second))
		var err error
		if value.Cancelled {
			err = status.Error(codes.Canceled, "operation_cancelled")
		}
		return &pb.OperationResult{Environment: report}, err
	}, nil)
	if err != nil {
		session.Close()
		return nil, err
	}
	c.environmentSession, c.environmentOperation = session, ack.OperationId
	return ack, nil
}

// StartEnvironmentSources preserves the original exact-IP source retry budget.
func (c *Controller) StartEnvironmentSources(ctx context.Context, req *pb.StartEnvironmentSourcesRequest) (*pb.OperationAck, error) {
	admit := func() (*pb.OperationAck, error) {
		c.environmentMu.Lock()
		defer c.environmentMu.Unlock()
		session := c.environmentSession
		if session == nil || session.Read().ID != req.SessionId {
			return nil, status.Error(codes.NotFound, "environment_session_expired")
		}
		if c.environmentSources != "" || !session.Read().Complete {
			return nil, c.environmentBusyLocked()
		}
		ack, err := c.startOwnedProbe(func(task context.Context, id string) (*pb.OperationResult, error) {
			bounded, cancel := context.WithTimeout(task, 20*time.Second)
			defer cancel()
			report := environmentReportProto(session.RetrySources(bounded))
			report.BrowserUrl = ""
			return &pb.OperationResult{Environment: report}, bounded.Err()
		}, func() { c.environmentMu.Lock(); c.environmentSources = ""; c.environmentMu.Unlock() })
		if err == nil {
			c.environmentSources = ack.OperationId
		}
		return ack, err
	}
	if req.Context != nil {
		return c.contextRequest(ctx, "environment.sources", req, req.Context, admit)
	}
	return c.appRequest(ctx, "environment.sources", req, "", admit)
}
func environmentReportProto(value environment.Snapshot) *pb.EnvironmentDiagnosticReport {
	return &pb.EnvironmentDiagnosticReport{SessionId: value.ID, BrowserUrl: value.BrowserURL, At: timestamppb.New(value.At), Mac: environmentSampleProto(value.Mac), Browser: environmentSampleProto(value.Browser), Complete: value.Complete}
}
func environmentSampleProto(value environment.Sample) *pb.EnvironmentSample {
	sample := &pb.EnvironmentSample{Ipv4: value.IPv4, Ipv6: value.IPv6, Complete: value.Complete, DnsError: value.DNSError, Languages: value.Languages, Region: value.Region, Timezone: value.Timezone, Browser: value.Browser, RtcAddresses: value.RTCAddresses, RtcState: value.RTCState, IpError: value.IPError}
	for _, check := range value.Checks {
		sample.Checks = append(sample.Checks, &pb.EnvironmentCheck{Kind: check.Kind, Target: check.Target, State: check.State, Value: check.Value, ErrorCode: check.ErrorCode, DurationMs: check.DurationMS, HttpStatus: check.HTTPStatus, At: timestamppb.New(check.At)})
	}
	for _, item := range value.Resolvers {
		sample.Resolvers = append(sample.Resolvers, &pb.EnvironmentResolver{Ip: item.IP, Country: item.Country, Organization: item.Organization})
	}
	if value.IPv4 != "" || value.IPv6 != "" {
		sample.Intelligence = networkDiagnosticProto(value.Intelligence)
	}
	return sample
}

// environmentMu is held; expose only the actual session owner identity.
func (c *Controller) environmentBusyLocked() error {
	id := c.environmentOperation
	if c.environmentSources != "" && c.environmentSources != "app" {
		id = c.environmentSources
	}
	metadata := map[string]string{"operation_id": id}
	c.mu.Lock()
	if op := c.operations[id]; op != nil {
		metadata["owner_client_id"] = op.OwnerClientId
	}
	c.mu.Unlock()
	st, err := status.New(codes.ResourceExhausted, "environment_busy").WithDetails(&errdetails.ErrorInfo{Reason: "environment_busy", Metadata: metadata})
	if err != nil {
		return status.Error(codes.ResourceExhausted, "environment_busy")
	}
	return st.Err()
}
