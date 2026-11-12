package controller

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/environment"
)

// InspectEnvironment never captures a selected route or binds a physical NIC.
// The singleton short-lived session is read explicitly, with no background polling.
func (c *Controller) InspectEnvironment(ctx context.Context, req *pb.EnvironmentDiagnosticRequest) (*pb.EnvironmentDiagnosticReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if req.Action == "sources" {
		c.environmentMu.Lock()
		session := c.environmentSession
		c.environmentMu.Unlock()
		if session == nil || session.Read().ID != req.SessionId {
			return nil, status.Error(codes.NotFound, "environment_session_expired")
		}
		bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		value := session.RetrySources(bounded)
		return environmentReportProto(value), nil
	}
	c.environmentMu.Lock()
	defer c.environmentMu.Unlock()
	switch req.Action {
	case "start":
		if c.tests == nil {
			return nil, status.Error(codes.Unavailable, "environment_unavailable")
		}
		if c.environmentSession != nil {
			c.environmentSession.Close()
		}
		session, err := environment.Start(c.tests.lifetime, c.exits)
		if err != nil {
			return nil, status.Error(codes.Unavailable, "environment_listener_failed")
		}
		c.environmentSession = session
	case "read", "cancel":
		if c.environmentSession == nil || c.environmentSession.Read().ID != req.SessionId {
			return nil, status.Error(codes.NotFound, "environment_session_expired")
		}
		if req.Action == "cancel" {
			c.environmentSession.Close()
		}
	default:
		return nil, status.Error(codes.InvalidArgument, "environment_action_invalid")
	}
	value := c.environmentSession.Read()
	return environmentReportProto(value), nil
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
