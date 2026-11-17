package controller

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/aiprofile"
)

// profileTasks owns admitted work across RPC/stdio disconnects. Close drains it
// before the Profile manager closes; the common controller retains all results.
type profileJob struct {
	done   chan struct{}
	cancel context.CancelFunc
}

type profileTasks struct {
	mu       sync.Mutex
	closed   bool
	cancel   context.CancelFunc
	lifetime context.Context
	jobs     map[string]profileJob
	wg       sync.WaitGroup
}

// Close cancels directory reads and drains admitted configuration transactions.
func (s *AIProfilesServer) Close() {
	s.tasks.mu.Lock()
	s.tasks.closed = true
	s.tasks.cancel()
	s.tasks.mu.Unlock()
	s.tasks.wg.Wait()
}

func (s *AIProfilesServer) startProfile(ctx context.Context, kind string, req proto.Message, identity *pb.OperationContext, run func(context.Context, string) (*pb.AIProfileOperationResult, error), discover bool) (*pb.OperationAck, error) {
	if s.controller == nil || identity == nil {
		return nil, status.Error(codes.Unimplemented, "capability_unavailable")
	}
	return s.controller.contextRequest(ctx, kind, req, identity, func() (*pb.OperationAck, error) {

		id, err := operationID()
		if err != nil {
			return nil, status.Error(codes.Internal, "internal_error")
		}
		s.tasks.mu.Lock()
		defer s.tasks.mu.Unlock()
		if s.tasks.closed {
			return nil, status.Error(codes.Unavailable, "daemon_stopping")
		}
		if len(s.tasks.jobs) >= 4 {
			return nil, status.Error(codes.ResourceExhausted, "operation_busy")
		}
		done := make(chan struct{})
		task, cancel := context.WithCancel(s.tasks.lifetime)
		job := profileJob{done: done}
		if kind == "profile.models" {
			job.cancel = cancel
		}
		s.tasks.jobs[id] = job
		s.tasks.wg.Add(1)
		go func() {
			defer s.tasks.wg.Done()
			defer cancel()
			// Once admitted, short local commits do not inherit the caller's deadline.
			runCtx := context.WithoutCancel(task)
			if kind == "profile.models" {
				runCtx = task
			}
			result, e := run(runCtx, id)
			code := ""
			if e != nil {
				code = status.Convert(e).Message()
				if status.Code(e) == codes.Unknown {
					code = status.Convert(aiError(e)).Message()
				}
				if kind == "profile.models" && task.Err() != nil {
					code = "operation_cancelled"
				}
			}
			params := []string{}
			if code == "ai_profile_conflict" {
				for _, detail := range status.Convert(e).Details() {
					if v, ok := detail.(*errdetails.ErrorInfo); ok && v.Metadata["latest_revision"] != "" {
						params = append(params, v.Metadata["latest_revision"])
					}
				}
			}
			s.controller.completeOperationPayload(id, "Profile操作", code, "", "", nil, &pb.OperationResult{AiProfile: result}, params...)
			close(done)
			// Auto-discovery is independent of the already frozen save/import result.
			if discover && e == nil && result != nil {
				for _, p := range result.GetWorkspace().GetProfiles() {
					if p.Id == result.ProfileId && p.Kind != "native" && p.ModelsState == "" {
						if _, err := s.manager.RefreshModelsBounded(s.tasks.lifetime, p.Id, (1<<20)-16384); err != nil && s.tasks.lifetime.Err() == nil {
							slog.Warn("Profile directory result unavailable", "operation_id", id, "code", status.Convert(aiError(err)).Message())
						}
					}
				}
			}
			s.tasks.mu.Lock()
			delete(s.tasks.jobs, id)
			s.tasks.mu.Unlock()
		}()
		return &pb.OperationAck{OperationId: id}, nil
	})
}

func (s *AIProfilesServer) awaitProfile(ctx context.Context, ack *pb.OperationAck) (*pb.AIProfileOperationResult, error) {
	s.tasks.mu.Lock()
	done := s.tasks.jobs[ack.OperationId].done
	s.tasks.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return nil, status.FromContextError(ctx.Err()).Err()
		}
	}
	op, err := s.controller.GetOperation(ctx, &pb.GetOperationRequest{DaemonInstance: s.controller.instance, Identity: &pb.GetOperationRequest_OperationId{OperationId: ack.OperationId}})
	if err != nil {
		return nil, err
	}
	if op.Error != nil {
		if op.Error.Code == "ai_profile_conflict" && len(op.Error.MessageParams) > 0 {
			return nil, profileConflictError(op.Error.MessageParams[0])
		}
		return nil, status.Error(codes.FailedPrecondition, op.Error.Code)
	}
	if op.GetAiProfile() == nil {
		return nil, status.Error(codes.Unavailable, "ai_commit_uncertain")
	}
	return op.GetAiProfile(), nil
}

// StartAIProfileModels admits a bounded directory read under the daemon owner.
func (s *AIProfilesServer) StartAIProfileModels(ctx context.Context, r *pb.AIProfileModelsRequest) (*pb.OperationAck, error) {
	r = proto.Clone(r).(*pb.AIProfileModelsRequest)
	return s.startProfile(ctx, "profile.models", r, r.Context, func(task context.Context, id string) (*pb.AIProfileOperationResult, error) {
		w, err := s.manager.RefreshModelsBounded(task, r.ProfileId, (1<<20)-16384)
		return &pb.AIProfileOperationResult{Workspace: aiWorkspace(w), OperationId: id}, err
	}, false)
}

func (s *AIProfilesServer) trackedMutation(ctx context.Context, r *pb.AIProfileMutation) (*pb.AIProfilesWorkspace, error) {
	copy := proto.Clone(r).(*pb.AIProfileMutation)
	copy.Context = nil
	copy.OperationId = "" // only the common owner generates operation IDs
	ack, err := s.startProfile(ctx, "profile.mutate", copy, r.Context, func(task context.Context, id string) (*pb.AIProfileOperationResult, error) {
		copy.OperationId = id
		w, e := s.mutateProfile(task, copy, true)
		result := &pb.AIProfileOperationResult{Workspace: w, OperationId: id}
		switch a := copy.Action.(type) {
		case *pb.AIProfileMutation_Save:
			result.ProfileId = a.Save.GetProfile().GetId()
		case *pb.AIProfileMutation_ImportProfile:
			result.ProfileId = a.ImportProfile.Id
		case *pb.AIProfileMutation_Delete:
			result.DeletedProfileId = a.Delete.ProfileId
		}
		return result, e
	}, true)
	if err != nil {
		return nil, err
	}
	result, err := s.awaitProfile(ctx, ack)
	return result.GetWorkspace(), err
}

func (s *AIProfilesServer) trackedPlan(ctx context.Context, r *pb.AIProfilePlanRequest) (*pb.AIProfilePlan, error) {
	r = proto.Clone(r).(*pb.AIProfilePlanRequest)
	ack, err := s.startProfile(ctx, "profile.plan", r, r.Context, func(task context.Context, id string) (*pb.AIProfileOperationResult, error) {
		copy := proto.Clone(r).(*pb.AIProfilePlanRequest)
		copy.Context = nil
		p, e := s.PlanAIProfileApply(task, copy)
		return &pb.AIProfileOperationResult{Plan: p, OperationId: id}, e
	}, false)
	if err != nil {
		return nil, err
	}
	result, err := s.awaitProfile(ctx, ack)
	return result.GetPlan(), err
}

func (s *AIProfilesServer) trackedApply(ctx context.Context, r *pb.AIProfileApplyRequest) (*pb.AIProfilesWorkspace, error) {
	copy := proto.Clone(r).(*pb.AIProfileApplyRequest)
	copy.Context = nil
	copy.OperationId = ""
	ack, err := s.startProfile(ctx, "profile.apply", copy, r.Context, func(task context.Context, id string) (*pb.AIProfileOperationResult, error) {
		copy.OperationId = id
		w, e := s.manager.Apply(task, aiprofile.ApplyRequest{PlanID: copy.PlanId, OperationID: id, ResultLimit: (1 << 20) - 16384})
		outcome := "applied"
		if e != nil {
			outcome = ""
			if strings.Contains(e.Error(), "ai_recovery_required") || strings.Contains(e.Error(), "ai_commit_uncertain") {
				outcome = "recovery_required"
			}
		}
		return &pb.AIProfileOperationResult{Workspace: aiWorkspace(w), Outcome: outcome, OperationId: id}, e
	}, false)
	if err != nil {
		return nil, err
	}
	result, err := s.awaitProfile(ctx, ack)
	return result.GetWorkspace(), err
}
