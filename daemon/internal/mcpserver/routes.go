package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"
	pb "pax-connect/daemon/gen/proto/paxv1"
)

func validateRouteArgs(name string, args map[string]any) error {
	var walk func(any) error
	walk = func(value any) error {
		switch v := value.(type) {
		case map[string]any:
			for key, item := range v {
				if key == "name" || key == "source_name" {
					if text, ok := item.(string); ok && strings.TrimSpace(text) == "" {
						return invalidArgument()
					}
				}
				if strings.Contains(key, "revision") {
					text, ok := item.(string)
					if !ok {
						return invalidArgument()
					}
					n, err := strconv.ParseUint(text, 10, 64)
					if err != nil || n == 0 && (name != "pax_logs_retention" || key != "expected_revision") {
						return invalidArgument()
					}
				}
				if err := walk(item); err != nil {
					return err
				}
			}
		case []any:
			for _, item := range v {
				if err := walk(item); err != nil {
					return err
				}
			}
		}
		return nil
	}
	// Revisions and byte limits are validated at the MCP boundary, document shape
	// by the pinned schema and again by the private business owner.
	copy := jsonCopy(args)
	delete(copy, "document")
	if err := walk(copy); err != nil {
		return err
	}
	if doc, ok := args["document"]; ok {
		raw, _ := json.Marshal(doc)
		if len(raw) > 64<<10 {
			return invalidArgument()
		}
	}

	return nil
}
func (s *Server) operationContext(args map[string]any) *pb.OperationContext {
	return &pb.OperationContext{ClientId: s.options.ClientID, RequestId: stringArg(args, "request_id", ""), ExpectedDaemonInstance: stringArg(args, "daemon_instance", "")}
}
func rev(args map[string]any, key string) uint64 { value, _ := countArg(args, key); return value }
func stringList(value any) []string {
	out := []string{}
	if items, ok := value.([]any); ok {
		for _, v := range items {
			if text, ok := v.(string); ok {
				out = append(out, text)
			}
		}
	}
	return out
}

// ready establishes observation before any intent, without keeping jobs in MCP.
func (b *backend) ready(ctx context.Context) (*pb.Snapshot, error) {
	snap, err := b.rpc.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
	if err != nil {
		return nil, err
	}
	watchCtx, stop := context.WithCancel(ctx)
	defer stop()
	stream, err := b.rpc.Watch(watchCtx, &pb.WatchRequest{FromSeq: snap.SnapshotVersion})
	if err != nil {
		return nil, err
	}
	frame, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	if frame.GetSnapshot() == nil {
		return nil, errors.New("protocol_mismatch")
	}
	return frame.GetSnapshot(), nil
}
func (s *Server) routeCall(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	b, err := s.open(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if b.conn != nil {
			_ = b.conn.Close()
		}
	}()
	if !slicesContain(b.hello.Capabilities, "routes.edit_view.v1") {
		return nil, errors.New("capability_unavailable")
	}
	var data map[string]any
	if name == "pax_route_get" {
		view, e := b.rpc.GetRouteEditView(ctx, &pb.GetRouteEditorRequest{RouteId: stringArg(args, "route_id", "")})
		if e != nil {
			return nil, e
		}
		data = map[string]any{"route_id": view.RouteId, "revision": revisionText(view.Revision), "config_revision": revisionText(view.ConfigRevision), "name": scrub(view.Name, false), "hidden": view.Hidden, "source_removed": view.SourceRemoved}
		if view.Chain != nil {
			data["chain"] = map[string]any{"front_id": view.Chain.FrontId, "final_id": view.Chain.FinalId}
		} else {
			var doc map[string]any
			if json.Unmarshal([]byte(view.DocumentJson), &doc) != nil {
				return nil, errors.New("redaction_unsupported")
			}
			data["document"] = doc
			credentials := []any{}
			for _, item := range view.Credentials {
				credentials = append(credentials, map[string]any{"path": item.Path, "present": item.Present})
			}
			data["credentials"] = credentials
		}
	} else {
		if stringArg(args, "daemon_instance", "") != b.hello.DaemonInstance {
			return nil, errors.New("daemon_restarted")
		}
		snap, e := b.ready(ctx)
		if e != nil {
			return nil, e
		}
		identity := s.operationContext(args)
		if name == "pax_routes_preview_cancel" {
			_, err = b.rpc.CancelRoutesPreview(ctx, &pb.CancelRoutesPreviewRequest{Token: stringArg(args, "token", ""), Context: identity})
			data = map[string]any{"released": true}
		} else {
			// Readiness may take five seconds; the actual asynchronous admission is bounded to two.
			admitCtx, stop := context.WithTimeout(ctx, 2*time.Second)
			defer stop()
			ack, e := s.routeMutation(admitCtx, b, snap, name, args, identity)
			if e != nil {
				return nil, e
			}
			state, e := b.rpc.GetOperation(ctx, &pb.GetOperationRequest{DaemonInstance: b.hello.DaemonInstance, Identity: &pb.GetOperationRequest_OperationId{OperationId: ack.OperationId}})
			if e != nil {
				return nil, e
			}
			data = operationData(state)
		}
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
func revisionText(v uint64) string { return strconv.FormatUint(v, 10) }
func (s *Server) routeMutation(ctx context.Context, b *backend, snap *pb.Snapshot, name string, args map[string]any, identity *pb.OperationContext) (*pb.OperationAck, error) {
	routeID := stringArg(args, "route_id", "")
	revision := rev(args, "expected_revision")
	switch name {
	case "pax_route_add", "pax_route_update":
		raw, _ := json.Marshal(args["document"])
		req := &pb.SaveRouteEditRequest{RouteId: routeID, ExpectedRevision: revision, Name: strings.TrimSpace(stringArg(args, "name", "")), DocumentJson: string(raw), Context: identity}
		req.SecretUpdates = routeSecretUpdates(args)
		return b.rpc.SaveRouteEdit(ctx, req)
	case "pax_routes_import":
		return b.rpc.ConfirmImportRoutes(ctx, &pb.ConfirmImportRoutesRequest{Token: stringArg(args, "token", ""), ItemIds: stringList(args["item_ids"]), ExpectedCatalogRevision: rev(args, "expected_catalog_revision"), Context: identity})
	case "pax_route_delete":
		return b.rpc.DeleteRoute(ctx, &pb.DeleteRouteRequest{RouteId: routeID, ExpectedRevision: revision, Context: identity})
	case "pax_route_tls":
		change, err := tlsRequest(snap, args)
		if err != nil {
			return nil, err
		}
		change.Context = identity
		return b.rpc.UpdateRouteTLS(ctx, change)
	case "pax_routes_tls_batch":
		req := &pb.ConfirmRouteTLSBatchRequest{Context: identity}
		for _, v := range args["routes"].([]any) {
			change, err := tlsRequest(snap, v.(map[string]any))
			if err != nil {
				return nil, err
			}
			req.Routes = append(req.Routes, change)
		}
		return b.rpc.ConfirmRouteTLSBatch(ctx, req)
	case "pax_route_groups_save":
		req := &pb.SaveRouteGroupsRequest{Context: identity, ExpectedCatalogRevision: rev(args, "expected_catalog_revision")}
		for _, v := range args["groups"].([]any) {
			item := v.(map[string]any)
			req.Groups = append(req.Groups, &pb.RouteGroup{Id: stringArg(item, "id", ""), Name: stringArg(item, "name", ""), MemberIds: stringList(item["member_ids"])})
		}
		return b.rpc.SaveRouteGroups(ctx, req)
	case "pax_route_chain_save":
		return b.rpc.SaveChainRoute(ctx, &pb.SaveChainRouteRequest{Id: stringArg(args, "id", ""), Name: stringArg(args, "name", ""), FrontId: stringArg(args, "front_id", ""), FinalId: stringArg(args, "final_id", ""), ExpectedRevision: revision, FrontRevision: rev(args, "front_revision"), FinalRevision: rev(args, "final_revision"), Context: identity})
	case "pax_subscription_restore_route":
		return b.rpc.RestoreSubscriptionRoute(ctx, &pb.RestoreSubscriptionRouteRequest{RouteId: routeID, ExpectedRevision: revision, Context: identity})
	case "pax_subscription_delete":
		return b.rpc.DeleteSubscription(ctx, &pb.DeleteSubscriptionRequest{SubscriptionId: stringArg(args, "subscription_id", ""), ExpectedSourceRevision: rev(args, "expected_source_revision"), ExpectedCatalogRevision: rev(args, "expected_catalog_revision"), Context: identity})
	case "pax_subscription_refresh":
		req := &pb.RefreshSubscriptionRequest{SubscriptionId: stringArg(args, "subscription_id", ""), ExpectedSourceRevision: rev(args, "expected_source_revision"), ReplacementUrl: stringArg(args, "replacement_url", ""), UseLastChannel: stringArg(args, "channel", "") == "last", Context: identity}
		if value, ok := args["user_agent"].(string); ok {
			req.UserAgent = &value
		}
		if route, ok := args["route"].(map[string]any); ok {
			req.RouteId = stringArg(route, "route_id", "")
			req.ExpectedRouteRevision = rev(route, "expected_revision")
			req.ExpectedConfigRevision = rev(route, "expected_config_revision")
		}
		return b.rpc.RefreshSubscription(ctx, req)
	}
	return nil, errors.New("invalid_argument")
}
func tlsRequest(_ *pb.Snapshot, args map[string]any) (*pb.UpdateRouteTLSRequest, error) {
	mode := pb.TLSMode_TLS_MODE_STRICT
	if stringArg(args, "mode", "") == "skip_verify" {
		mode = pb.TLSMode_TLS_MODE_SKIP_VERIFY
		if !boolArg(args, "acknowledge_skip") {
			return nil, errors.New("tls_confirmation_required")
		}
	}
	return &pb.UpdateRouteTLSRequest{RouteId: stringArg(args, "route_id", ""), ExpectedRouteRevision: rev(args, "expected_revision"), IdentityToken: stringArg(args, "identity_token", ""), Mode: mode, ConfirmSkip: boolArg(args, "acknowledge_skip")}, nil
}

func retryCode(code string) string {
	if code == "query_changed" || code == "ai_usage_busy" || code == "ai_usage_unavailable" || code == "ai_catalog_unavailable" {
		return "read"
	}
	if code == "ai_target_changed" || strings.HasSuffix(code, "stale") || strings.HasSuffix(code, "conflict") || code == "tls_identity_changed" || code == "daemon_restarted" {
		return "replan"
	}
	if strings.HasSuffix(code, "expired") {
		return "read"
	}
	if code == "settings_commit_uncertain" || code == "ai_commit_uncertain" || code == "ai_request_timeout" {
		return "read"
	}
	if code == "timeout" || code == "daemon_unavailable" {
		return "same_request"
	}
	return "never"
}
func writeFailure(err error, args map[string]any) *mcp.CallToolResult {
	code := errorCode(err)
	out := failure(code, retryCode(code))
	data := out.StructuredContent.(map[string]any)
	data["request_id"] = stringArg(args, "request_id", "")
	data["daemon_instance"] = stringArg(args, "daemon_instance", "")
	if code == "core_required" {
		data["details"] = map[string]any{"required_action": "open_pax_and_enable_proxy", "authorization_owner": "app"}
	}
	for _, detail := range status.Convert(err).Details() {
		if value, ok := detail.(*errdetails.ErrorInfo); ok && (strings.HasSuffix(value.Reason, "conflict") || strings.HasSuffix(value.Reason, "stale")) {
			if n, e := strconv.ParseUint(value.Metadata["latest_revision"], 10, 64); e == nil {
				data["latest_revision"] = revisionText(n)
			}
		}
		if value, ok := detail.(*errdetails.ErrorInfo); ok && value.Reason == "environment_busy" {
			details := map[string]any{}
			for _, key := range []string{"operation_id", "owner_client_id"} {
				if v := value.Metadata[key]; v != "" && len(v) <= 128 {
					details[key] = v
				}
			}
			data["details"] = details
		}
	}
	return toolResult(data, true)
}

func routeSecretUpdates(args map[string]any) []*pb.RouteSecretUpdate {
	updates := []*pb.RouteSecretUpdate{}
	if items, ok := args["secret_updates"].([]any); ok {
		for _, v := range items {
			item := v.(map[string]any)
			update := &pb.RouteSecretUpdate{Path: stringArg(item, "path", "")}
			if value, ok := item["value"].(string); ok {
				update.Change = &pb.RouteSecretUpdate_Value{Value: value}
			} else {
				update.Change = &pb.RouteSecretUpdate_Clear{Clear: boolArg(item, "clear")}
			}
			updates = append(updates, update)
		}
	}
	return updates
}
