package controller

import (
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

func subscriptionAttemptProto(v *config.SubscriptionAttempt) *pb.SubscriptionAttempt {
	if v == nil {
		return nil
	}
	return &pb.SubscriptionAttempt{At: timestamppb.New(v.At), Channel: v.Channel, Phase: v.Phase, HttpStatus: v.HTTPStatus, QueueMs: v.QueueMS, DnsMs: v.DNSMS, ConnectMs: v.ConnectMS, TlsMs: v.TLSMS, HeadersMs: v.HeadersMS, BodyMs: v.BodyMS, TotalMs: v.TotalMS, ErrorCode: v.Error}
}
