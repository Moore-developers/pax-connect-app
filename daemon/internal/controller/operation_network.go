package controller

import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	pb "pax-connect/daemon/gen/proto/paxv1"
)

// Called with c.mu held, at the product owner's actual completion boundary.
func (c *Controller) operationOutcomeLocked(result *pb.OperationResult, label string) {
	switch label {
	case "开启代理", "关闭代理", "切换线路":
		result.Proxy = &pb.ProxyOperationResult{Status: c.current.Status, SelectedRouteId: c.current.SelectedRouteId, RunningRouteId: c.current.GetCurrentRoute().GetId()}
		for _, reason := range c.current.DegradedReasons {
			result.Proxy.DegradedReasons = append(result.Proxy.DegradedReasons, proto.Clone(reason).(*pb.ProductError))
		}
	case "验证当前通道":
		if c.current.CurrentPathCheck != nil {
			result.CurrentPath = proto.Clone(c.current.CurrentPathCheck).(*pb.CurrentPathCheck)
		}
	}
	if value := c.measurementOutcomes[result.OperationId]; value != nil {
		for _, item := range value.Items {
			if item.Measurement == nil && item.Error == nil {
				code := "measurement_unavailable"
				if !result.Ok {
					code = "operation_cancelled"
				}
				item.Error = &pb.ProductError{Code: code}
			}
		}
		result.Measurements = value
		delete(c.measurementOutcomes, result.OperationId)
	}
}

// GetOperation calls this without the snapshot lock. Each original owner remains
// authoritative for cancellation and live progress; no adapter task registry exists.
func (c *Controller) observeOperation(op *pb.OperationState) {
	if op.State != "accepted" && op.State != "running" {
		return
	}
	op.Progress = &pb.OperationProgress{Phase: "queued"}
	if c.cf != nil && strings.HasPrefix(op.Kind, "cf.") {
		c.cf.mu.Lock()
		if w := c.cf.workspace; w != nil && w.operation == op.OperationId {
			op.State = "running"
			op.Cancellable = w.cancel != nil
			completed, total := uint64(w.completed), uint64(w.total)
			op.Progress = &pb.OperationProgress{Phase: w.phase, Completed: &completed, Total: &total}
		}
		c.cf.mu.Unlock()
	}
	if s := c.profileOperations; s != nil && strings.HasPrefix(op.Kind, "profile.") {
		s.tasks.mu.Lock()
		job, ok := s.tasks.jobs[op.OperationId]
		s.tasks.mu.Unlock()
		if ok {
			op.State = "running"
			op.Progress.Phase = "running"
			op.Cancellable = job.cancel != nil
		}
	}
	if c.proxy != nil {
		c.proxy.mu.Lock()
		active := c.proxy.activeIntent.Load()
		op.Cancellable = op.Cancellable || c.proxy.routeCancels[op.OperationId] != nil
		if active != nil && *active == op.OperationId {
			op.State = "running"
			op.Progress.Phase = "running"

		}
		c.proxy.mu.Unlock()
	}
	if c.subs != nil {
		c.subs.mu.Lock()
		op.Cancellable = op.Cancellable || c.subs.ops[op.OperationId] != nil
		c.subs.mu.Unlock()
	}
	if c.tests != nil {
		c.tests.mu.Lock()
		op.Cancellable = op.Cancellable || c.tests.ops[op.OperationId] != nil
		if op.Kind == "cf.validate" || strings.HasPrefix(op.Kind, "diagnostic.") || strings.HasPrefix(op.Kind, "environment.") {
			if c.tests.ops[op.OperationId] != nil {
				op.State = "running"
				op.Progress.Phase = "running"
			}
		}
		for _, progress := range c.tests.progress {
			if progress.OperationId == op.OperationId || c.tests.parents[progress.OperationId] == op.OperationId {
				op.State = "running"
				op.Progress.Phase = progress.Phase.String()
			}
		}
		c.tests.mu.Unlock()
	}
	if op.Kind == "path.recheck" {
		op.Cancellable = true
		op.State = "running"
		op.Progress.Phase = "running"
	}
	if op.Kind == "diagnostic.network" && c.pathDiagnostics != nil {
		p := c.pathDiagnostics
		p.progressMu.Lock()
		if v := p.progress; v != nil && v.OperationId == op.OperationId {
			completed, total := uint64(v.ProbesCompleted), uint64(v.ProbesTotal)
			op.Progress.Phase = "probes:" + v.ExitState + ":" + v.SourcesState
			op.Progress.Completed = &completed
			if total > 0 {
				op.Progress.Total = &total
			}
		}
		p.progressMu.Unlock()
	}
	if strings.HasPrefix(op.Kind, "test-route.") {
		c.mu.Lock()
		if value := c.measurementOutcomes[op.OperationId]; value != nil {
			total, completed := uint64(len(value.Items)), uint64(0)
			for _, item := range value.Items {
				if item.Measurement != nil || item.Error != nil {
					completed++
				}
			}
			op.Progress.Completed, op.Progress.Total = &completed, &total
			if completed > 0 {
				op.State = "running"
			}
		}
		c.mu.Unlock()
	}
}

// StartConnectionDiagnostic runs the existing bounded diagnosis under its owner.
func (c *Controller) StartConnectionDiagnostic(ctx context.Context, req *pb.StartConnectionDiagnosticRequest) (*pb.OperationAck, error) {
	admit := func() (*pb.OperationAck, error) {
		return c.startDiagnostic(func(task context.Context, id string) (*pb.OperationResult, error) {
			value, err := c.diagnoseConnection(task, &pb.ConnectionDiagnosticRequest{Recheck: req.Recheck})
			return &pb.OperationResult{Connection: value}, err
		})
	}
	if req.Context != nil {
		return c.contextRequest(ctx, "diagnostic.connection", req, req.Context, admit)
	}
	return c.appRequest(ctx, "diagnostic.connection", req, "", admit)
}

// StartNetworkDiagnostic shares the synchronous App's admission, budget and probes.
func (c *Controller) StartNetworkDiagnostic(ctx context.Context, req *pb.StartNetworkDiagnosticRequest) (*pb.OperationAck, error) {
	if req.Mode != "quick" && req.Mode != "full" && req.Mode != "services" && req.Mode != "sources" {
		return nil, status.Error(codes.InvalidArgument, "network_diagnostic_mode_invalid")
	}
	admit := func() (*pb.OperationAck, error) {
		return c.startDiagnostic(func(task context.Context, id string) (*pb.OperationResult, error) {
			value, err := c.diagnoseNetwork(task, &pb.NetworkDiagnosticRequest{Mode: req.Mode, OperationId: id})
			return &pb.OperationResult{Network: value}, err
		})
	}
	if req.Context != nil {
		return c.contextRequest(ctx, "diagnostic.network", req, req.Context, admit)
	}
	return c.appRequest(ctx, "diagnostic.network", req, "", admit)
}

func (c *Controller) startDiagnostic(run func(context.Context, string) (*pb.OperationResult, error)) (*pb.OperationAck, error) {
	p, owner := c.pathDiagnostics, c.tests
	if p == nil || owner == nil {
		return nil, status.Error(codes.Unimplemented, "network_diagnostics_unavailable")
	}
	if !p.mu.TryLock() {
		return nil, status.Error(codes.ResourceExhausted, "path_diagnostics_busy")
	}
	ack, err := c.startOwnedProbe(run, p.mu.Unlock)
	if err != nil {
		p.mu.Unlock()
	}
	return ack, err
}

// testOwner already owns probe lifetime, shutdown draining and cancellation.
// after runs before completion publication, releasing shared admission.
func (c *Controller) startOwnedProbe(run func(context.Context, string) (*pb.OperationResult, error), after func()) (*pb.OperationAck, error) {
	owner := c.tests
	if owner == nil {
		return nil, status.Error(codes.Unavailable, "environment_unavailable")
	}
	id, err := operationID()
	if err != nil {
		return nil, status.Error(codes.Internal, "internal_error")
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.closed {
		return nil, status.Error(codes.Unavailable, "daemon_stopping")
	}
	task, cancel := context.WithCancel(owner.lifetime)
	owner.ops[id] = cancel
	owner.probes.Add(1)
	go func() {
		defer owner.probes.Done()
		defer cancel()
		result, err := run(task, id)
		if result == nil {
			result = &pb.OperationResult{}
		}
		code := ""
		if task.Err() != nil {
			code = "operation_cancelled"
		} else if err != nil {
			if status.Code(err) == codes.Unknown {
				code, _ = catalogFailure(err)
			} else {
				code = status.Convert(err).Message()
			}
		}
		owner.mu.Lock()
		delete(owner.ops, id)
		owner.mu.Unlock()
		if after != nil {
			after()
		}
		c.completeOperationPayload(id, "网络检测", code, "", "", nil, result)
	}()
	return &pb.OperationAck{OperationId: id}, nil
}

// A runtime commit can no longer be interrupted or advertised as cancellable.
func (c *Controller) markOperationCommitted(id string) {
	c.proxy.mu.Lock()
	delete(c.proxy.routeCancels, id)
	c.proxy.mu.Unlock()
}
