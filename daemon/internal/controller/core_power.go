package controller

import (
	"context"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/telemetry"
	"time"
)

// NotifyPowerEvent serializes state and history in the existing owner. A cancelled
// caller never reads fields concurrently with a still-running owner closure.
func (c *Controller) NotifyPowerEvent(ctx context.Context, req *pb.PowerEventRequest) (*emptypb.Empty, error) {
	results := make(chan error, 1)
	sleeping, sequence := req.Sleeping, req.Sequence
	received := time.Now()
	err := c.query(ctx, func(operation context.Context) {
		if sequence <= c.powerSequence {
			results <- nil
			return
		}
		started := time.Now()
		c.cancelCurrentPath("power_changed")
		if sleeping {
			c.proxy.sleeping = true
			c.mu.Lock()
			if check := c.current.CurrentPathCheck; check != nil {
				check.Cause = "device_sleeping"
				c.publishCurrentPathLocked()
			}
			c.mu.Unlock()
		}
		var failure error
		if core, ok := c.coreSession.(interface {
			Power(context.Context, bool, uint64) error
		}); ok {
			failure = core.Power(operation, sleeping, sequence)
		}
		if failure == nil {
			c.powerSequence = sequence
			c.proxy.powerSequence = sequence
			c.proxy.sleeping = sleeping
			if !sleeping && c.proxy.desired.Load() && c.proxy.running.valid() {
				c.beginPathCheck("", "wake", true, received)
			}
		}
		phase, name := "wake", "系统唤醒"
		if sleeping {
			phase, name = "sleep", "系统休眠"
		}
		code, result, level := "", "completed", "info"
		if failure != nil {
			code, result, level = "core_power_failed", "failed", "error"
		}
		telemetry.RecordDiagnostic(telemetry.Diagnostic{Stage: "core_power", Phase: phase, Code: code, Success: failure == nil, DurationMS: time.Since(started).Milliseconds()})
		c.enqueueHistoryEvent(config.HistoryEvent{ID: fmt.Sprintf("power:%s:%d:%d", phase, sequence, time.Now().UnixNano()), At: time.Now(), Kind: phase, Title: name, Source: "Pax", Level: level, System: &config.SystemEvent{Type: "runtime." + phase, Name: name, Category: config.EventCategory{Key: "runtime", Name: "网络与后台", Symbol: "network"}, Result: result, ErrorCode: code}})
		results <- failure
	})
	if err != nil {
		return nil, err
	}
	select {
	case failure := <-results:
		if failure != nil {
			return nil, status.Error(codes.Unavailable, "core_power_failed")
		}
		return &emptypb.Empty{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
