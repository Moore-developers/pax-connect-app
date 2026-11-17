package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
)

func validateProfileArgs(args map[string]any) error {
	if err := validateRouteArgs("", args); err != nil {
		return err
	}
	for _, field := range []string{"profile", "native_profile"} {
		if p, ok := args[field].(map[string]any); ok {
			if values, ok := p["model_multipliers"].(map[string]any); ok {
				for key := range values {
					if len(key) > 200 || strings.TrimSpace(key) == "" {
						return invalidArgument()
					}
				}
			}
		}
	}
	return nil
}
func profileWorkspace(value *pb.AIProfilesWorkspace) map[string]any {
	out, _ := projection(value)
	for _, raw := range out["bindings"].([]any) {
		delete(raw.(map[string]any), "templateRevision")
	}
	return out
}
func profileResult(value *pb.AIProfileOperationResult) map[string]any {
	out := map[string]any{}
	if value.Workspace != nil {
		out["workspace"] = profileWorkspace(value.Workspace)
	}
	if value.Plan != nil {
		out["plan"], _ = projection(value.Plan)
	}
	for key, v := range map[string]string{"profile_id": value.ProfileId, "deleted_profile_id": value.DeletedProfileId, "outcome": value.Outcome, "operation_id": value.OperationId} {
		if v != "" {
			out[key] = v
		}
	}
	return out
}
func (s *Server) profilesQuery(ctx context.Context, b *backend) (map[string]any, string, string, error) {
	if !slicesContain(b.hello.Capabilities, "ai.profiles.operations.v1") {
		return nil, "", "", errors.New("capability_unavailable")
	}
	w, err := b.profiles.QueryAIProfiles(ctx, &pb.AIProfilesQuery{})
	if err != nil {
		return nil, "", "", err
	}
	return profileWorkspace(w), "", "", nil
}
func (s *Server) profileCall(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	b, err := s.open(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if b.conn != nil {
			_ = b.conn.Close()
		}
	}()
	if !slicesContain(b.hello.Capabilities, "ai.profiles.operations.v1") {
		return nil, errors.New("capability_unavailable")
	}
	if stringArg(args, "daemon_instance", "") != b.hello.DaemonInstance {
		return nil, errors.New("daemon_restarted")
	}
	if cli, ok := args["cli"].(string); ok && cli != "codex" && cli != "claude" {
		return nil, errors.New("ai_template_unsupported")
	}
	if _, err = b.ready(ctx); err != nil {
		return nil, err
	}
	identity := s.operationContext(args)
	var data map[string]any
	switch name {
	case "pax_profile_models_refresh":
		admit, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		ack, e := b.profiles.StartAIProfileModels(admit, &pb.AIProfileModelsRequest{ProfileId: stringArg(args, "profile_id", ""), Context: identity})
		if e != nil {
			return nil, e
		}
		op, e := b.rpc.GetOperation(ctx, operationQuery(b, ack.OperationId))
		if e != nil {
			return nil, e
		}
		data = operationData(op)
	case "pax_profile_plan":
		p, e := b.profiles.PlanAIProfileApply(ctx, &pb.AIProfilePlanRequest{Cli: stringArg(args, "cli", ""), ProfileId: stringArg(args, "profile_id", ""), Revision: rev(args, "revision"), InspectionId: stringArg(args, "inspection_id", ""), Initialize: boolArg(args, "initialize"), ModelId: stringArg(args, "model_id", ""), Context: identity})
		if e != nil {
			return nil, e
		}
		data, err = projection(p)
	default:
		if name == "pax_profile_apply" {
			_, err = b.profiles.ApplyAIProfile(ctx, &pb.AIProfileApplyRequest{PlanId: stringArg(args, "plan_id", ""), Context: identity})
		} else {
			req := &pb.AIProfileMutation{Context: identity}
			switch name {
			case "pax_profile_save":
				p, ok := args["profile"].(map[string]any)
				if !ok {
					p = args["native_profile"].(map[string]any)
				}
				summary := &pb.AIProfileSummary{Id: stringArg(p, "id", ""), Name: stringArg(p, "name", ""), BaseUrl: stringArg(p, "base_url", ""), Multiplier: p["multiplier"].(float64), ModelIds: stringList(p["model_ids"]), ModelMultipliers: map[string]float64{}}
				if models, ok := p["model_multipliers"].(map[string]any); ok {
					for k, v := range models {
						summary.ModelMultipliers[k] = v.(float64)
					}
				}
				save := &pb.AIProfileSave{Profile: summary, ExpectedRevision: rev(args, "expected_revision")}
				if key, ok := args["key_change"].(map[string]any); ok {
					save.ReplaceKey = stringArg(key, "mode", "") == "replace"
					save.ApiKey = stringArg(key, "api_key", "")
				}
				req.Action = &pb.AIProfileMutation_Save{Save: save}
			case "pax_profile_import":
				req.Action = &pb.AIProfileMutation_ImportProfile{ImportProfile: &pb.AIProfileImport{Cli: stringArg(args, "cli", ""), InspectionId: stringArg(args, "inspection_id", ""), Id: stringArg(args, "id", ""), Name: stringArg(args, "name", ""), Multiplier: args["multiplier"].(float64)}}
			case "pax_profile_delete":
				req.Action = &pb.AIProfileMutation_Delete{Delete: &pb.AIProfileDelete{ProfileId: stringArg(args, "profile_id", ""), ExpectedRevision: rev(args, "expected_revision")}}
			}
			_, err = b.profiles.MutateAIProfile(ctx, req)
		}
		if err != nil {
			return nil, err
		}
		op, e := b.rpc.GetOperation(ctx, &pb.GetOperationRequest{DaemonInstance: b.hello.DaemonInstance, Identity: &pb.GetOperationRequest_Request{Request: &pb.OperationRequestIdentity{ClientId: identity.ClientId, RequestId: identity.RequestId}}})
		if e != nil {
			return nil, e
		}
		if op.GetAiProfile() == nil {
			return nil, errors.New("ai_commit_uncertain")
		}
		data = profileResult(op.GetAiProfile())
	}
	if err != nil {
		return nil, err
	}
	out := envelope(b.hello.DaemonInstance, data)
	out["timezone"] = b.hello.Timezone
	raw, _ := json.Marshal(out)
	if len(raw) > responseLimit {
		return nil, errors.New("result_too_large")
	}
	return out, nil
}

var profileErrorCodes = map[string]bool{}

func init() {
	for _, code := range strings.Fields(`result_too_large ai_profile_invalid ai_profile_conflict ai_profile_in_use ai_template_unsupported ai_target_changed ai_target_unsafe ai_plan_expired ai_store_invalid ai_commit_uncertain ai_recovery_required ai_request_cancelled ai_request_timeout models_timeout models_not_found models_invalid_response models_auth_failed models_unavailable`) {
		profileErrorCodes[code] = true
	}
}
