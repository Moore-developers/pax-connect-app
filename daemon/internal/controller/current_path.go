package controller

import (
	"context"
	"errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"time"
)

type pathCheckJob struct {
	ctx         context.Context
	checkID     string
	binding     runningBinding
	runtime     uint64
	power       uint64
	waitNetwork bool
	wakeAt      time.Time
	trigger     string
}
type pathCheckDone struct {
	job        pathCheckJob
	result     config.CurrentPathResult
	network    string
	generation uint64
	progress   bool
}
type pathCheckRequest struct {
	operation, route  string
	revision, runtime uint64
	power             uint64
}

type pathCheckState struct {
	id          string
	cancel      context.CancelFunc
	commands    []string
	runtime     uint64
	binding     runningBinding
	waitNetwork bool
}

func (c *Controller) publishCurrentPathLocked() {
	c.snapshotSeq++
	c.current.SnapshotVersion = c.snapshotSeq
	check := proto.Clone(c.current.CurrentPathCheck).(*pb.CurrentPathCheck)
	c.fanoutLocked(&pb.WatchEvent{Seq: c.snapshotSeq, SnapshotVersion: c.snapshotSeq, Payload: &pb.WatchEvent_CurrentPathCheck{CurrentPathCheck: check}})
}
func (c *Controller) cancelCurrentPath(cause string) {
	p := c.proxy
	if p == nil {
		return
	}
	p.wakeFollowup = nil
	state := p.pathCheck
	p.pathCheck = nil
	p.mu.Lock()
	cancel := p.pathCancel
	p.pathCancel = nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if state != nil {
		state.cancel()
		for _, id := range state.commands {
			c.completeOperation(id, "验证当前通道", "operation_cancelled")
		}
	}
	c.mu.Lock()
	if check := c.current.CurrentPathCheck; check != nil && check.State != "idle" && (check.State != "stale" || check.Cause != cause) {
		check.State = "stale"
		check.Cause = cause
		c.publishCurrentPathLocked()
	}
	c.mu.Unlock()
}

func (c *Controller) startCurrentPath(p config.RouteConfig, operation string) {
	c.bindCurrentPath(p)
	c.beginPathCheck(operation, "on", false, time.Time{})
}

func (c *Controller) beginCurrentPath(operation string) {
	c.proxy.wakeFollowup = nil
	c.beginPathCheck(operation, "manual", false, time.Time{})
}

func (c *Controller) beginPathCheck(operation, trigger string, waitNetwork bool, wakeAt time.Time) {
	binding := c.proxy.running
	if !binding.valid() || c.proxy.sleeping || !c.proxy.desired.Load() {
		return
	}
	if _, ok := c.coreSession.(interface {
		VerifyCurrentPath(context.Context, config.CurrentPathRequest) (config.CurrentPathResult, error)
	}); !ok {
		return
	}
	c.cancelCurrentPath("new_operation")
	id, err := operationID()
	if err != nil {
		return
	}
	timeout := 7 * time.Second
	if waitNetwork {
		timeout = 16 * time.Second
	}
	ctx, cancel := context.WithTimeout(c.proxy.lifetime, timeout)
	state := &pathCheckState{id: id, cancel: cancel, runtime: c.proxy.runtimeEpoch, binding: binding, waitNetwork: waitNetwork}
	c.proxy.pathCheck = state
	c.proxy.mu.Lock()
	c.proxy.pathCancel = cancel
	c.proxy.mu.Unlock()
	started := time.Now()
	if waitNetwork {
		started = wakeAt
	}
	c.mu.Lock()
	c.current.CurrentPathCheck = &pb.CurrentPathCheck{CheckId: id, TriggerOperationId: operation, RouteId: binding.route, ConfigRevision: binding.revision, State: pathInitialState(waitNetwork), StartedAt: timestamppb.New(started), EvidenceScope: "current_mixed_egress"}
	c.publishCurrentPathLocked()
	c.mu.Unlock()
	job := pathCheckJob{ctx: ctx, checkID: id, binding: binding, runtime: state.runtime, power: c.proxy.powerSequence, waitNetwork: waitNetwork, wakeAt: wakeAt, trigger: trigger}
	select {
	case c.proxy.pathJobs <- job:
	default:
		select {
		case <-c.proxy.pathJobs:
		default:
		}
		select {
		case c.proxy.pathJobs <- job:
		default:
			cancel()
		}
	}
}
func (c *Controller) finishCurrentPath(done pathCheckDone) {
	p := c.proxy
	state := p.pathCheck
	if state == nil || state.id != done.job.checkID || state.runtime != p.runtimeEpoch || done.job.power != p.powerSequence {
		return
	}
	if done.job.ctx.Err() == context.Canceled || !p.desired.Load() {
		c.cancelCurrentPath("operation_cancelled")
		return
	}
	if done.progress {
		if c.physical != nil {
			sample, _ := c.physical.localSnapshot()
			if sample.generation != done.generation {
				c.cancelCurrentPath("network_changed")
				return
			}
			p.networkGeneration = done.generation
		}
		state.waitNetwork = false
		c.mu.Lock()
		c.current.CurrentPathCheck.State = done.result.State
		c.current.CurrentPathCheck.StartedAt = timestamppb.New(done.result.StartedAt)
		c.publishCurrentPathLocked()
		c.mu.Unlock()
		return
	}
	result := done.result
	if done.network != "" && c.physical != nil {
		sample, _ := c.physical.localSnapshot()
		if sample.generation != done.generation || sample.epoch != done.network {
			result.State = "stale"
			result.Cause = "network_changed"
		}
	}
	if result.CompletedAt.IsZero() {
		result.CompletedAt = time.Now()
	}
	c.mu.Lock()
	running := c.current.GetCurrentRoute().GetId()
	enabled := c.current.Status != pb.ProtectionStatus_PROTECTION_STATUS_OFF
	c.mu.Unlock()
	if !enabled || running != state.binding.route || done.job.ctx.Err() == context.Canceled {
		c.cancelCurrentPath("runtime_changed")
		return
	}
	if result.State == "" {
		result.State = "inconclusive"
		result.Cause = "current_path_unavailable"
	}
	if done.job.waitNetwork && result.Cause == "physical_network_wait" && !p.sleeping && p.desired.Load() {
		p.wakeFollowup = &wakeFollowup{binding: state.binding, sequence: p.powerSequence, at: done.job.wakeAt}
	}
	state.cancel()
	p.mu.Lock()
	p.pathCancel = nil
	p.mu.Unlock()
	p.pathCheck = nil
	c.mu.Lock()
	check := c.current.CurrentPathCheck
	if check == nil || check.CheckId != state.id {
		c.mu.Unlock()
		return
	}
	check.State = result.State
	check.Cause = result.Cause
	check.CoreSession = result.CoreSession
	check.RuntimeVersion = result.RuntimeVersion
	check.NetworkEpoch = done.network
	check.CompletedAt = timestamppb.New(result.CompletedAt)
	c.publishCurrentPathLocked()
	c.mu.Unlock()
	title, level := "暂未确认当前通道连通", "info"
	if result.State == "passed" {
		title = "当前通道可用"
	}
	if result.State == "failed" {
		title, level = "当前通道检查失败", "warning"
	}
	if result.State == "stale" {
		title = "当前通道证据已失效"
	}
	started := result.StartedAt
	evidence := &config.SystemEvent{Type: "verification.current_path", Name: "验证当前通道", Category: config.EventCategory{Key: "verification", Name: "通道验证", Symbol: "checkmark.shield"}, Result: result.State, ErrorCode: result.Cause, Sections: []config.EventSection{{Kind: "objects", Title: "检查对象", Fields: []config.EventField{{Label: "线路", Value: state.binding.name}}}, {Kind: "facts", Title: "当次检查结果", Fields: []config.EventField{{Label: "结论", Value: title}, {Label: "触发来源", Value: done.job.trigger}}}}}
	if !started.IsZero() {
		evidence.StartedAt = &started
	}
	c.enqueueHistoryEvent(config.HistoryEvent{System: evidence, ID: "current-path:" + state.id, At: result.CompletedAt, Kind: "current_path", Title: title, Level: level, Source: "Pax", Result: result.State})
	for _, id := range state.commands {
		failure := ""
		if result.State != "passed" {
			failure = "check_inconclusive"
			if result.State == "failed" {
				failure = "check_failed"
			}
		}
		c.completeOperation(id, "验证当前通道", failure)
	}
}

// RecheckCurrentPath accepts an explicit verification without starting a core or changing routes.
func (c *Controller) RecheckCurrentPath(ctx context.Context, q *pb.CurrentPathRecheckRequest) (*pb.OperationAck, error) {
	if q.RequestId == "" || len(q.RequestId) > 128 {
		return nil, status.Error(codes.InvalidArgument, "current_path_invalid")
	}
	var ack *pb.OperationAck
	var failure error
	err := c.query(ctx, func(ctx context.Context) {
		if c.proxy.pathRequests == nil {
			c.proxy.pathRequests = map[string]pathCheckRequest{}
		}
		c.mu.Lock()
		route := c.current.GetCurrentRoute().GetId()
		revision := c.current.GetCurrentPathCheck().GetConfigRevision()
		on := c.current.Status != pb.ProtectionStatus_PROTECTION_STATUS_OFF
		c.mu.Unlock()
		if previous, ok := c.proxy.pathRequests[q.RequestId]; ok {
			if previous.route != route || previous.revision != revision || previous.runtime != c.proxy.runtimeEpoch || previous.power != c.proxy.powerSequence || !on {
				failure = errors.New("current_path_request_changed")
				return
			}
			ack = &pb.OperationAck{OperationId: previous.operation}
			return
		}
		if !on || route == "" {
			failure = errors.New("no_active_core")
			return
		}
		c.proxy.wakeFollowup = nil
		if c.proxy.pathCheck == nil {
			c.beginCurrentPath("")
		}
		state := c.proxy.pathCheck
		if state == nil {
			failure = errors.New("current_path_unavailable")
			return
		}
		if len(state.commands) >= 8 {
			failure = errors.New("operation_busy")
			return
		}
		id, e := operationID()
		if e != nil {
			failure = e
			return
		}
		state.commands = append(state.commands, id)
		if len(c.proxy.pathRequests) >= 128 {
			c.proxy.pathRequests = map[string]pathCheckRequest{}
		}
		revision = state.binding.revision
		c.proxy.pathRequests[q.RequestId] = pathCheckRequest{operation: id, route: route, revision: revision, runtime: c.proxy.runtimeEpoch, power: c.proxy.powerSequence}
		c.mu.Lock()
		if check := c.current.CurrentPathCheck; check != nil {
			check.CommandOperationIds = append(check.CommandOperationIds, id)
			c.publishCurrentPathLocked()
		}
		c.mu.Unlock()
		ack = &pb.OperationAck{OperationId: id}
	})
	if err != nil {
		return nil, err
	}
	if failure != nil {
		return nil, status.Error(codes.FailedPrecondition, failure.Error())
	}
	return ack, nil
}

func pathInitialState(wait bool) string {
	if wait {
		return "waiting_network"
	}
	return "checking"
}
