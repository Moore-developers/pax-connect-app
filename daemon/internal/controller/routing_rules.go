package controller

import (
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"time"
)

type routingRulesReader interface {
	ReadRoutingRules(context.Context) (config.RoutingRulesSnapshot, error)
}

// QueryRoutingRules serializes the read with profile changes; no network mutation is admitted.
func (c *Controller) QueryRoutingRules(ctx context.Context, _ *emptypb.Empty) (*pb.RoutingRulesSnapshot, error) {
	var value config.RoutingRulesSnapshot
	var failure error
	err := c.query(ctx, func(ctx context.Context) {
		c.mu.Lock()
		state := c.current.Status
		c.mu.Unlock()
		if state == pb.ProtectionStatus_PROTECTION_STATUS_OFF {
			value = config.RoutingRulesSnapshot{State: "configured", Rules: config.ConfiguredRoutingRules(), At: time.Now()}
			return
		}
		reader, ok := c.coreSession.(routingRulesReader)
		if !ok {
			failure = status.Error(codes.Unimplemented, "routing_rules_unavailable")
			return
		}
		if state != pb.ProtectionStatus_PROTECTION_STATUS_PROTECTED && state != pb.ProtectionStatus_PROTECTION_STATUS_DEGRADED {
			value = config.RoutingRulesSnapshot{State: "updating", At: time.Now()}
			return
		}
		value, failure = reader.ReadRoutingRules(ctx)
		if state == pb.ProtectionStatus_PROTECTION_STATUS_DEGRADED && value.State == "active" {
			value.State = "last_committed"
		}
	})
	if err != nil {
		return nil, status.Error(codes.Unavailable, "routing_rules_unavailable")
	}
	if failure != nil {
		return nil, status.Error(codes.Unavailable, "routing_rules_unavailable")
	}
	out := &pb.RoutingRulesSnapshot{State: value.State, At: timestamppb.New(value.At)}
	for _, r := range value.Rules {
		out.Rules = append(out.Rules, &pb.RoutingRule{Id: r.ID, Order: r.Order, Match: r.Match, Action: r.Action, Source: r.Source})
	}
	return out, nil
}
