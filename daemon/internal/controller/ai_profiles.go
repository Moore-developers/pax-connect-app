package controller

import (
	"context"
	"errors"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/aiprofile"
	"strconv"
)

// AIProfilesServer maps user-only RPCs to the independent CLI configuration owner.
type AIProfilesServer struct {
	pb.UnimplementedAIProfilesServiceServer
	manager    *aiprofile.Manager
	controller *Controller
	tasks      profileTasks
}

// NewAIProfilesServer never registers with the root core service.
func NewAIProfilesServer(m *aiprofile.Manager, owners ...*Controller) *AIProfilesServer {
	lifetime, cancel := context.WithCancel(context.Background())
	s := &AIProfilesServer{manager: m, tasks: profileTasks{lifetime: lifetime, cancel: cancel, jobs: map[string]profileJob{}}}
	if len(owners) > 0 {
		s.controller = owners[0]
		s.controller.profileOperations = s
	}
	return s
}

// ConfigureAIProfiles announces only a successfully composed user configuration service.
func (c *Controller) ConfigureAIProfiles() { c.aiProfiles = true }
func aiError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return status.Error(codes.Canceled, "ai_request_cancelled")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, "ai_request_timeout")
	}
	var failure aiprofile.Failure
	if errors.As(err, &failure) {
		return status.Error(codes.FailedPrecondition, string(failure))
	}
	return status.Error(codes.Internal, "ai_store_invalid")
}
func aiWorkspace(w aiprofile.Workspace) *pb.AIProfilesWorkspace {
	out := &pb.AIProfilesWorkspace{}
	for _, p := range w.Profiles {
		summary := &pb.AIProfileSummary{Id: p.ID, Revision: p.Revision, ConnectionRevision: p.ConnectionRevision, Name: p.Name, BaseUrl: p.BaseURL, Multiplier: p.Multiplier, ModelIds: p.ModelIDs, HasKey: p.HasKey, Kind: p.Kind, NativeCli: string(p.NativeCLI), ModelsState: p.ModelsState, ModelsEndpoint: p.ModelsEndpoint, ModelsChecked: p.ModelsChecked, ModelsError: p.ModelsError, ModelMultipliers: p.ModelMultipliers}
		for _, v := range p.Catalog {
			summary.Catalog = append(summary.Catalog, &pb.AIProviderModel{Id: v.ID, Owner: v.Owner, DisplayName: v.Name, ContextWindow: v.ContextWindow})
		}
		out.Profiles = append(out.Profiles, summary)
	}
	for _, b := range w.Bindings {
		out.Bindings = append(out.Bindings, &pb.AIClientBinding{Cli: string(b.CLI), ProfileId: b.ProfileID, State: b.State, Reason: b.Reason, InspectionId: b.InspectionID, TemplateRevision: b.TemplateRevision, Endpoint: b.Endpoint, TargetPath: b.TargetPath, RequiresModel: b.RequiresModel, ServiceSource: b.ServiceSource})
	}
	return out
}

// QueryAIProfiles returns only redacted metadata and native file status.
func (s *AIProfilesServer) QueryAIProfiles(ctx context.Context, _ *pb.AIProfilesQuery) (*pb.AIProfilesWorkspace, error) {
	w, e := s.manager.Query(ctx)
	return aiWorkspace(w), aiError(e)
}

// MutateAIProfile does not write native CLI files.
func (s *AIProfilesServer) MutateAIProfile(ctx context.Context, r *pb.AIProfileMutation) (*pb.AIProfilesWorkspace, error) {
	if r.Context != nil {
		return s.trackedMutation(ctx, r)
	}
	return s.mutateProfile(ctx, r, false)
}
func (s *AIProfilesServer) mutateProfile(ctx context.Context, r *pb.AIProfileMutation, deferModels bool) (*pb.AIProfilesWorkspace, error) {
	m := aiprofile.Mutation{OperationID: r.OperationId, DeferModels: deferModels}
	if deferModels {
		m.ResultLimit = (1 << 20) - 16384
	}
	switch a := r.Action.(type) {
	case *pb.AIProfileMutation_Save:
		if a.Save.Profile == nil {
			return nil, aiError(aiprofile.Invalid)
		}
		p := a.Save.Profile
		m.Action = "save"
		m.Profile = aiprofile.Profile{ID: p.Id, Name: p.Name, BaseURL: p.BaseUrl, Multiplier: p.Multiplier, ModelIDs: p.ModelIds, ModelMultipliers: p.ModelMultipliers}
		m.ExpectedRevision = a.Save.ExpectedRevision
		if a.Save.ReplaceKey {
			if a.Save.ApiKey == "" {
				return nil, aiError(aiprofile.Invalid)
			}
			m.Key = a.Save.ApiKey
		}
	case *pb.AIProfileMutation_Delete:
		m.Action = "delete"
		m.Profile.ID = a.Delete.ProfileId
		m.ExpectedRevision = a.Delete.ExpectedRevision
	case *pb.AIProfileMutation_ImportProfile:
		m.Action = "import"
		m.CLI = aiprofile.CLI(a.ImportProfile.Cli)
		m.InspectionID = a.ImportProfile.InspectionId
		m.Profile = aiprofile.Profile{ID: a.ImportProfile.Id, Name: a.ImportProfile.Name, Multiplier: a.ImportProfile.Multiplier}
	case *pb.AIProfileMutation_InitializeTemplate:
		m.Action = "initialize_template"
		m.CLI = aiprofile.CLI(a.InitializeTemplate.Cli)
		m.InspectionID = a.InitializeTemplate.InspectionId
	default:
		return nil, aiError(aiprofile.Invalid)
	}
	w, e := s.manager.Mutate(ctx, m)
	return aiWorkspace(w), s.profileError(ctx, e, m.Profile.ID)
}

// PlanAIProfileApply freezes a preview without returning native bytes.
func (s *AIProfilesServer) PlanAIProfileApply(ctx context.Context, r *pb.AIProfilePlanRequest) (*pb.AIProfilePlan, error) {
	if r.Context != nil {
		return s.trackedPlan(ctx, r)
	}
	p, e := s.manager.Plan(ctx, aiprofile.PlanRequest{CLI: aiprofile.CLI(r.Cli), ProfileID: r.ProfileId, Revision: r.Revision, TemplateRevision: r.TemplateRevision, InspectionID: r.InspectionId, Restore: r.Restore, Initialize: r.Initialize, ModelID: r.ModelId})
	return &pb.AIProfilePlan{Id: p.ID, Cli: string(p.CLI), ProfileId: p.ProfileID, BaseUrl: p.BaseURL, ExpiresUnix: p.ExpiresAt.Unix(), Initializes: p.Initializes, TargetPath: p.TargetPath}, s.profileError(ctx, e, r.ProfileId)
}

// ApplyAIProfile returns the durable group result, not an admission ACK.
func (s *AIProfilesServer) ApplyAIProfile(ctx context.Context, r *pb.AIProfileApplyRequest) (*pb.AIProfilesWorkspace, error) {
	if r.Context != nil {
		return s.trackedApply(ctx, r)
	}
	w, e := s.manager.Apply(ctx, aiprofile.ApplyRequest{PlanID: r.PlanId, OperationID: r.OperationId})
	return aiWorkspace(w), aiError(e)
}

// RefreshAIProfileModels queries only the saved provider, never a model generation endpoint.
func (s *AIProfilesServer) RefreshAIProfileModels(ctx context.Context, r *pb.AIProfileModelsRequest) (*pb.AIProfilesWorkspace, error) {
	w, e := s.manager.RefreshModels(ctx, r.ProfileId)
	return aiWorkspace(w), aiError(e)
}

// A conflict reports the latest observed revision, without disclosing credentials.
func (s *AIProfilesServer) profileError(ctx context.Context, err error, id string) error {
	mapped := aiError(err)
	if !errors.Is(err, aiprofile.Conflict) || id == "" {
		return mapped
	}
	w, e := s.manager.Query(ctx)
	if e != nil {
		return mapped
	}
	for _, p := range w.Profiles {
		if p.ID == id {
			return profileConflictError(strconv.FormatUint(p.Revision, 10))
		}
	}
	return mapped
}
func profileConflictError(revision string) error {
	out, e := status.New(codes.FailedPrecondition, "ai_profile_conflict").WithDetails(&errdetails.ErrorInfo{Reason: "ai_profile_conflict", Metadata: map[string]string{"latest_revision": revision}})
	if e != nil {
		return status.Error(codes.FailedPrecondition, "ai_profile_conflict")
	}
	return out.Err()
}
