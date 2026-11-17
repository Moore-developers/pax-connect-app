package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"
	pb "pax-connect/daemon/gen/proto/paxv1"
)

func validateCFArgs(name string, args map[string]any) error {
	if err := validateRouteArgs(name, args); err != nil {
		return err
	}

	if name == "pax_cf_run" {
		action := stringArg(args, "action", "")
		ips := stringList(args["ips"])
		_, present := args["ips"]
		if action == "scan" && present || action != "scan" && len(ips) == 0 || strings.HasSuffix(action, "_one") && len(ips) != 1 {
			return invalidArgument()
		}
	}
	return nil
}
func (s *Server) cfQuery(ctx context.Context, b *backend, p *queryPage) (map[string]any, string, string, error) {
	if !slicesContain(b.hello.Capabilities, "routes.cf.operations.v1") {
		return nil, "", "", errors.New("capability_unavailable")
	}
	req := &pb.CFWorkspaceRequest{Network: stringArg(p.args, "network", "")}
	if target, ok := p.args["route"].(map[string]any); ok {
		req.RouteId = stringArg(target, "route_id", "")
		req.ExpectedRevision = rev(target, "expected_route_revision")
		req.ExpectedConfigRevision = rev(target, "expected_config_revision")
	}
	value, err := b.rpc.QueryCFWorkspace(ctx, req)
	if err != nil {
		return nil, "", "", err
	}
	workspaceAt := time.Now().UTC().Format(time.RFC3339Nano)
	candidates, err := b.rpc.QueryCFCandidates(ctx, &emptypb.Empty{})
	if err != nil {
		return nil, "", "", err
	}
	workspace, _ := projection(value)
	candidateData, _ := projection(candidates)
	return map[string]any{"workspace": workspace, "candidates": candidateData, "workspace_at": workspaceAt, "candidates_at": time.Now().UTC().Format(time.RFC3339Nano)}, "", "", nil
}
func (s *Server) cfCall(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	b, err := s.open(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if b.conn != nil {
			_ = b.conn.Close()
		}
	}()
	if !slicesContain(b.hello.Capabilities, "routes.cf.operations.v1") {
		return nil, errors.New("capability_unavailable")
	}
	if stringArg(args, "daemon_instance", "") != b.hello.DaemonInstance {
		return nil, errors.New("daemon_restarted")
	}
	if _, err = b.ready(ctx); err != nil {
		return nil, err
	}
	admit, stop := context.WithTimeout(ctx, 2*time.Second)
	defer stop()
	identity := s.operationContext(args)
	var ack *pb.OperationAck
	switch name {
	case "pax_cf_run":
		ack, err = b.rpc.RunCFTask(admit, &pb.CFTaskRequest{Action: stringArg(args, "action", ""), Ips: stringList(args["ips"]), Context: identity})
	case "pax_cf_validate":
		req := &pb.StartCFValidationRequest{RouteId: stringArg(args, "route_id", ""), ExpectedRouteRevision: rev(args, "expected_route_revision"), ExpectedConfigRevision: rev(args, "expected_config_revision"), Ip: stringArg(args, "ip", ""), Context: identity}
		if document, ok := args["document"]; ok {
			raw, _ := json.Marshal(document)
			text := string(raw)
			req.DocumentJson = &text
		}
		req.SecretUpdates = routeSecretUpdates(args)
		if chain, ok := args["chain"].(map[string]any); ok {
			req.ChainId, req.ChainRevision = stringArg(chain, "id", ""), rev(chain, "revision")
		}
		ack, err = b.rpc.StartCFValidation(admit, req)
	case "pax_cf_save":
		ack, err = b.rpc.SaveValidatedCFRoute(admit, &pb.SaveValidatedCFRouteRequest{RouteId: stringArg(args, "route_id", ""), ExpectedRouteRevision: rev(args, "expected_route_revision"), ExpectedConfigRevision: rev(args, "expected_config_revision"), ValidationId: stringArg(args, "validation_id", ""), Context: identity})
	}
	if err != nil {
		return nil, err
	}
	state, err := b.rpc.GetOperation(ctx, operationQuery(b, ack.OperationId))
	if err != nil {
		return nil, err
	}
	out := envelope(b.hello.DaemonInstance, operationData(state))
	out["timezone"] = b.hello.Timezone
	return out, nil
}
func cfOperationResult(op *pb.OperationState) map[string]any {
	if v := op.GetCfWorkspace(); v != nil {
		out, _ := projection(v)
		return out
	}
	if v := op.GetCfValidation(); v != nil {
		out := map[string]any{"network": v.Network, "route_revision": revisionText(v.RouteRevision), "config_revision": revisionText(v.ConfigRevision), "success": v.Success}
		if v.Candidate != nil {
			out["candidate"], _ = projection(v.Candidate)
		}
		if v.Success {
			out["validation_id"], out["draft_digest"], out["expires_at"] = v.ValidationId, v.DraftDigest, v.ExpiresAt.AsTime().Format(time.RFC3339Nano)
		}
		return out
	}
	return nil
}

var cfErrorCodes = map[string]bool{}

func init() {
	for _, v := range strings.Fields(`cf_unavailable cf_workspace_unavailable cf_busy cf_task_busy cf_ip_invalid cf_candidate_limit cf_action_invalid cf_cache_invalid cf_cache_save_failed cf_tcp_unreachable cf_task_failed cf_validation_failed cf_validation_expired network_observation_unavailable`) {
		cfErrorCodes[v] = true
	}
}
