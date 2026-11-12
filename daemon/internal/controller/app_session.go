package controller

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// ShutdownAppSession acknowledges a drain request. Socket/job disappearance confirms completion.
func (c *Controller) ShutdownAppSession(ctx context.Context, _ *emptypb.Empty) (*emptypb.Empty, error) {
	c.mu.Lock()
	stop := c.sessionStop
	c.mu.Unlock()
	if stop == nil {
		return nil, status.Error(codes.FailedPrecondition, "not_app_owned_session")
	}
	drain, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	if err := c.CloseProxy(drain); err != nil {
		return nil, status.Error(codes.Unavailable, "network_restore_incomplete")
	}
	stop()
	return &emptypb.Empty{}, nil
}

// ConfigureUserMode marks the unprivileged composition and binds its App lifetime.
// Core lifecycle is separate; product intents use ACK/OperationResult.
func (c *Controller) ConfigureUserMode(stop context.CancelFunc, ownerAppPID uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.userMode = true
	c.ownerAppPID = ownerAppPID
	c.sessionStop = stop
}
