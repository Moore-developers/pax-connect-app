package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	pb "pax-connect/daemon/gen/proto/paxv1"
)

func (s *Server) networkCall(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	b, err := s.open(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if b.conn != nil {
			_ = b.conn.Close()
		}
	}()
	if !slicesContain(b.hello.Capabilities, "network.operations.v1") {
		return nil, errors.New("capability_unavailable")
	}
	var data map[string]any
	if networkReadOnly(name) {
		switch name {
		case "pax_physical_egress":
			value, e := b.rpc.GetPhysicalEgress(ctx, &emptypb.Empty{})
			if e != nil {
				return nil, e
			}
			data, err = projection(value)
		case "pax_traffic_path":
			value, e := b.rpc.GetTrafficPathDiagnostics(ctx, &emptypb.Empty{})
			if e != nil {
				return nil, e
			}
			data, err = projection(value)
		case "pax_environment_read":
			value, e := b.rpc.InspectEnvironment(ctx, &pb.EnvironmentDiagnosticRequest{Action: "read", SessionId: stringArg(args, "session_id", "")})
			if e != nil {
				return nil, e
			}
			data = environmentData(value)
		}
	} else {
		if stringArg(args, "daemon_instance", "") != b.hello.DaemonInstance {
			return nil, errors.New("daemon_restarted")
		}
		if _, err = b.ready(ctx); err != nil {
			return nil, err
		}
		admit, stop := context.WithTimeout(ctx, 2*time.Second)
		defer stop()
		identity := s.operationContext(args)
		switch name {
		case "pax_operation_cancel":
			data, err = s.cancelNetworkOperation(ctx, admit, b, args, identity)
		case "pax_environment_start":
			value, e := b.rpc.InspectEnvironment(admit, &pb.EnvironmentDiagnosticRequest{Action: "start", Context: identity})
			if e != nil {
				return nil, e
			}
			op, e := b.rpc.GetOperation(ctx, operationQuery(b, value.OperationId))
			if e != nil {
				return nil, e
			}
			data = map[string]any{"operation": operationData(op), "session_id": value.SessionId, "browser_url": value.BrowserUrl, "expires_at": value.ExpiresAt.AsTime().Format(time.RFC3339Nano), "browser_required": true}
		default:
			ack, e := s.networkMutation(admit, b, name, args, identity)
			if e != nil {
				return nil, e
			}
			state, e := b.rpc.GetOperation(ctx, operationQuery(b, ack.OperationId))
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
func operationQuery(b *backend, id string) *pb.GetOperationRequest {
	return &pb.GetOperationRequest{DaemonInstance: b.hello.DaemonInstance, Identity: &pb.GetOperationRequest_OperationId{OperationId: id}}
}
func (s *Server) cancelNetworkOperation(ctx, admit context.Context, b *backend, args map[string]any, identity *pb.OperationContext) (map[string]any, error) {
	query := operationQuery(b, stringArg(args, "operation_id", ""))
	op, err := b.rpc.GetOperation(ctx, query)
	if err != nil {
		return nil, err
	}
	permission := cancellationPermission(op.Kind)
	if permission == "" {
		return nil, errors.New("not_cancellable")
	}
	if !s.allowed[permission] {
		return nil, errors.New("permission_denied")
	}
	if op.State == "unknown" {
		return nil, errors.New("operation_unknown")
	}
	terminal := op.State == "succeeded" || op.State == "failed" || op.State == "cancelled"
	if !terminal && !op.Cancellable {
		return nil, errors.New("not_cancellable")
	}
	_, err = b.rpc.CancelOperation(admit, &pb.CancelOperationRequest{OperationId: op.OperationId, Context: identity})
	if err != nil {
		return nil, err
	}
	latest, err := b.rpc.GetOperation(ctx, query)
	if err != nil {
		return nil, err
	}
	return map[string]any{"operation": operationData(latest), "cancel_requested": !terminal}, nil
}
func (s *Server) networkMutation(ctx context.Context, b *backend, name string, args map[string]any, identity *pb.OperationContext) (*pb.OperationAck, error) {
	switch name {
	case "pax_proxy_set":
		return b.rpc.SetProxyEnabled(ctx, &pb.SetProxyEnabledRequest{Enabled: boolArg(args, "enabled"), Context: identity})
	case "pax_route_select":
		return b.rpc.SelectRoute(ctx, &pb.SelectRouteRequest{RouteId: stringArg(args, "route_id", ""), ExpectedRouteRevision: rev(args, "expected_route_revision"), Context: identity})
	case "pax_routes_test":
		req := &pb.TestRouteRequest{Kind: stringArg(args, "kind", ""), Context: identity}
		for _, v := range args["targets"].([]any) {
			item := v.(map[string]any)
			req.Targets = append(req.Targets, &pb.RouteTestTarget{RouteId: stringArg(item, "route_id", ""), ExpectedRouteRevision: rev(item, "expected_route_revision"), ExpectedConfigRevision: rev(item, "expected_config_revision")})
		}
		return b.rpc.TestRoute(ctx, req)
	case "pax_path_recheck":
		return b.rpc.RecheckCurrentPath(ctx, &pb.CurrentPathRecheckRequest{Context: identity})
	case "pax_connection_diagnose":
		return b.rpc.StartConnectionDiagnostic(ctx, &pb.StartConnectionDiagnosticRequest{Recheck: boolArg(args, "recheck"), Context: identity})
	case "pax_network_diagnose":
		return b.rpc.StartNetworkDiagnostic(ctx, &pb.StartNetworkDiagnosticRequest{Mode: stringArg(args, "mode", "quick"), Context: identity})
	case "pax_environment_sources_retry":
		return b.rpc.StartEnvironmentSources(ctx, &pb.StartEnvironmentSourcesRequest{SessionId: stringArg(args, "session_id", ""), Context: identity})
	}
	return nil, errors.New("invalid_argument")
}
func environmentData(value *pb.EnvironmentDiagnosticReport) map[string]any {
	value = proto.Clone(value).(*pb.EnvironmentDiagnosticReport)
	value.BrowserUrl = ""
	out, _ := projection(value)
	delete(out, "browserUrl")
	browser := "pending"
	if value.GetBrowser().GetBrowser() != "" {
		browser = "observed"
		if value.Browser.IpError != "" || value.Browser.DnsError != "" {
			browser = "failed"
		}
	} else if value.Complete {
		browser = "not_detected"
	}
	out["dependencies"] = map[string]any{"app": map[string]any{"state": "available", "role": "daemon_owner"}, "browser": map[string]any{"state": browser, "required": true}, "system_metadata": map[string]any{"state": "not_detected", "source": "app_only"}}
	out["system_metadata"] = map[string]any{"languages": nil, "region": nil, "timezone": nil, "reason": "app_metadata_not_available_over_rpc"}
	return out
}
func networkOperationResult(op *pb.OperationState) map[string]any {
	if value := op.GetProxy(); value != nil {
		reasons := []any{}
		for _, reason := range value.DegradedReasons {
			reasons = append(reasons, map[string]any{"code": safeRouteCode(reason.Code)})
		}
		out := map[string]any{"status": value.Status.String(), "degraded_reasons": reasons}
		if value.SelectedRouteId != "" {
			out["selected_route_id"] = value.SelectedRouteId
		}
		if value.RunningRouteId != "" {
			out["running_route_id"] = value.RunningRouteId
		}
		return out
	}
	if value := op.GetMeasurements(); value != nil {
		items := []any{}
		for _, v := range value.Items {
			item := map[string]any{"route_id": v.RouteId, "config_revision": revisionText(v.ConfigRevision), "success": v.Success}
			if v.Error != nil {
				item["error"] = map[string]any{"code": safeRouteCode(v.Error.Code)}
			}
			var measurement proto.Message
			switch v.Measurement.(type) {
			case *pb.RouteMeasurementResult_Latency:
				measurement = v.GetLatency()
			case *pb.RouteMeasurementResult_Exit:
				measurement = v.GetExit()
			case *pb.RouteMeasurementResult_Download:
				measurement = v.GetDownload()
			}
			if measurement != nil {
				item["measurement"], _ = projection(measurement)
			}
			items = append(items, item)
		}
		return map[string]any{"items": items}
	}
	if value := op.GetEnvironment(); value != nil {
		return environmentData(value)
	}
	var result proto.Message
	switch op.Result.(type) {
	case *pb.OperationState_CurrentPath:
		result = op.GetCurrentPath()
	case *pb.OperationState_Connection:
		result = op.GetConnection()
	case *pb.OperationState_Network:
		result = op.GetNetwork()
	}
	if result != nil {
		out, _ := projection(result)
		return out
	}
	return nil
}

var networkErrorCodes = map[string]bool{}

func init() {
	for _, code := range strings.Fields(`core_required core_unavailable core_identity_mismatch core_profile_start_failed core_selection_unknown route_connections_pending runtime_binding_unconfirmed runtime_observation_unavailable runtime_switch_unavailable protection_unverified tun_conflict_other tun_start_failed stop_failed no_active_core proxy_off current_path_invalid current_path_request_changed current_path_unavailable check_failed check_inconclusive physical_network_unavailable path_inspection_unavailable path_diagnostics_busy path_diagnostics_unavailable diagnostic_busy network_diagnostic_busy network_diagnostics_unavailable network_diagnostic_mode_invalid diagnostic_operation_invalid boundary_changed network_changed capture_changed operation_unknown operation_not_found not_cancellable test_batch_invalid test_batch_partial test_unavailable test_kind_invalid route_test_timeout proxy_unreachable tls_certificate_invalid measurement_busy measurement_unavailable download_busy download_unavailable download_queue_full download_timeout download_incomplete download_failed exit_unavailable exit_query_failed exit_timeout exit_query_timeout exit_prepare_failed environment_busy environment_unavailable environment_listener_failed environment_session_expired result_too_large latency_samples_incomplete ipv6_unavailable daemon_stopping node_dns_failed node_tcp_failed node_tls_failed https_dns_failed https_tls_failed https_timeout https_http_failed provider_error provider_failed provider_timeout provider_unavailable node_connection_failed node_connection_refused node_connection_reset node_network_unreachable node_connection_closed node_connection_timeout target_connection_failed target_connection_refused target_connection_reset target_network_unreachable target_connection_closed target_connection_timeout target_dns_failed target_tls_failed target_http_failed test_target_invalid download_body_interrupted exit_invalid exit_failed exit_http_failed exit_unavailable exit_timeout exit_queue_timeout exit_provider_unavailable exit_provider_rate_limited exit_query_cancelled exit_geo_failed exit_geo_unavailable`) {
		networkErrorCodes[code] = true
	}
}
