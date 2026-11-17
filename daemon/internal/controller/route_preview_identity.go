package controller

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"time"
)

func (c *Controller) checkPreviewContext(identity *pb.OperationContext) error {
	if identity == nil {
		return nil
	}
	if !validOperationID(identity.ClientId) || !validOperationID(identity.RequestId) {
		return status.Error(codes.InvalidArgument, "request_invalid")
	}
	if identity.ExpectedDaemonInstance != c.instance {
		return status.Error(codes.FailedPrecondition, "daemon_restarted")
	}
	if !validRequestTime(identity.RequestId, time.Now()) {
		return status.Error(codes.InvalidArgument, "request_expired")
	}
	return nil
}
func (c *Controller) previousPreview(req *pb.PreviewRoutesRequest) (*pb.RoutesPreview, error) {
	if req.Context == nil {
		return nil, nil
	}
	for _, entry := range c.routes.previews {
		if entry.clientID == req.Context.ClientId && entry.requestID == req.Context.RequestId {
			if entry.digest != previewDigest(req) {
				return nil, status.Error(codes.InvalidArgument, "request_conflict")
			}
			if time.Now().After(entry.expires) {
				return nil, status.Error(codes.FailedPrecondition, "preview_expired")
			}
			return proto.Clone(entry.view).(*pb.RoutesPreview), nil
		}
	}
	return nil, nil
}
func previewDigest(req *pb.PreviewRoutesRequest) [32]byte {
	raw, _ := proto.MarshalOptions{Deterministic: true}.Marshal(req)
	return operationDigest("route.preview", raw)
}
