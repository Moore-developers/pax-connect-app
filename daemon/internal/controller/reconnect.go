package controller

import (
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	pb "pax-connect/daemon/gen/proto/paxv1"
)

// ReconnectProxy stays unadvertised; power recovery belongs to the native core.
func (c *Controller) ReconnectProxy(context.Context, *emptypb.Empty) (*pb.OperationAck, error) {
	return nil, status.Error(codes.Unimplemented, "runtime_switch_unavailable")
}
