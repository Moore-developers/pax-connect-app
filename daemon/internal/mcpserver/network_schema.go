package mcpserver

import pb "pax-connect/daemon/gen/proto/paxv1"

var networkTools = []string{"pax_operation_cancel", "pax_proxy_set", "pax_route_select", "pax_routes_test", "pax_path_recheck", "pax_physical_egress", "pax_traffic_path", "pax_connection_diagnose", "pax_network_diagnose", "pax_environment_start", "pax_environment_read", "pax_environment_sources_retry"}

func isNetworkTool(name string) bool {
	for _, v := range networkTools {
		if name == v {
			return true
		}
	}
	return false
}
func networkReadOnly(name string) bool {
	return name == "pax_physical_egress" || name == "pax_traffic_path" || name == "pax_environment_read"
}
func networkPermission(name string) string {
	if networkReadOnly(name) {
		return "read"
	}
	if name == "pax_proxy_set" || name == "pax_route_select" {
		return "network"
	}
	if name == "pax_operation_cancel" {
		return ""
	} // Checked against the actual retained owner kind.
	return "diagnose"
}
func (s *Server) canCancel() bool {
	for _, v := range []string{"network", "diagnose", "manage", "profiles", "settings", "delete"} {
		if s.allowed[v] {
			return true
		}
	}
	return false
}
func cancellationPermission(kind string) string {
	switch kind {
	case "proxy.set", "route.select":
		return "network"
	case "cf.run", "cf.validate", "test-route.latency", "test-route.exit", "test-route.download", "test-route.", "path.recheck", "diagnostic.connection", "diagnostic.network", "environment.start", "environment.sources":
		return "diagnose"
	case "profile.models":
		return "profiles"
	case "subscription.refresh":
		return "manage"
	default:
		return ""
	}
}
func networkDescription(name string) string {
	switch name {
	case "pax_operation_cancel":
		return "Request cancellation of an existing operation, including another client's/App operation. Requires that operation's actual action permission. Cancellation ACK is not the original terminal outcome; read it back. Cannot cancel App root authorization or committed transactions."
	case "pax_proxy_set":
		return "Enable or disable proxy through Pax's existing owner. Accepted is not success. core_required requires the user to open Pax and explicitly enable proxy there; authorization belongs to the App. Never supply sudo passwords or automatically replay after authorization."
	case "pax_route_select":
		return "Select a versioned route. The retained result distinguishes selected and actually running routes. Existing connection handling follows Pax runtime behavior."
	case "pax_routes_test":
		return "Run one bounded latency/exit/download batch on saved versioned routes, retaining each item. Uses fixed targets, existing concurrency and bandwidth budgets; download may consume up to 8 MiB per route. Does not switch routes or create TUN."
	case "pax_path_recheck":
		return "Verify the actual running mixed-egress path without starting core or selecting a route. This is distinct from isolated route latency and global TUN/DNS protection."
	case "pax_physical_egress":
		return "Read the daemon's existing physical egress sample and timestamp; no active remeasurement."
	case "pax_traffic_path":
		return "Read bounded local OS path observations without public probes. Partial/unavailable observations are explicit."
	case "pax_connection_diagnose":
		return "Start an owner-managed connection diagnosis. Default recheck=false reads retained attempt evidence; true runs bounded OS/core and one fixed HTTPS probe."
	case "pax_network_diagnose":
		return "Start a bounded owner-managed network diagnosis using a frozen saved route and fixed probes. quick is default; full/services/sources follow existing budgets. Poll operation for progress and terminal report."
	case "pax_environment_start":
		return "Start a 45-second environment session without requiring an App page. Returns one short-lived loopback URL for the user to open in a real browser. Existing session returns environment_busy. Missing browser/system metadata is not a successful leak check. URL must not be logged or persisted."
	case "pax_environment_read":
		return "Read an existing environment session without probing. Omits browser URL, marks browser dependency and unavailable App-only language/region/timezone metadata. Complete means the session ended, not that all checks passed."
	case "pax_environment_sources_retry":
		return "Start a bounded exact-IP source retry for an existing completed environment session. Preserves independent Mac/browser evidence; no arbitrary target URLs."
	}
	return ""
}
func networkInputSchema(name string) schema {
	props := schema{}
	required := []string{}
	switch name {
	case "pax_operation_cancel":
		props["operation_id"] = idSchema()
		required = []string{"operation_id"}
	case "pax_proxy_set":
		props["enabled"] = boolean()
		required = []string{"enabled"}
	case "pax_route_select":
		props = schema{"route_id": idSchema(), "expected_route_revision": revisionSchema()}
		required = []string{"route_id", "expected_route_revision"}
	case "pax_routes_test":
		target := object(schema{"route_id": idSchema(), "expected_route_revision": revisionSchema(), "expected_config_revision": revisionSchema()}, "route_id", "expected_route_revision", "expected_config_revision")
		props = schema{"kind": enum("latency", "exit", "download"), "targets": nonemptyArray(target, 100)}
		required = []string{"kind", "targets"}
	case "pax_connection_diagnose":
		props["recheck"] = boolean()
	case "pax_network_diagnose":
		props["mode"] = enum("full", "quick", "services", "sources")
	case "pax_environment_read", "pax_environment_sources_retry":
		props["session_id"] = idSchema()
		required = []string{"session_id"}
	}
	if !networkReadOnly(name) {
		identity := routeInputSchema("pax_routes_preview_cancel")["properties"].(schema)
		props["request_id"], props["daemon_instance"] = identity["request_id"], identity["daemon_instance"]
		required = append(required, "request_id", "daemon_instance")
	}
	out := object(props, required...)
	out["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	return out
}
func environmentSchema() schema {
	out := messageSchema(&pb.EnvironmentDiagnosticReport{})
	props := out["properties"].(schema)
	delete(props, "browserUrl")
	props["dependencies"] = object(schema{
		"app":             object(schema{"state": enum("available", "unavailable"), "role": enum("daemon_owner")}, "state", "role"),
		"browser":         object(schema{"state": enum("pending", "observed", "not_detected", "failed"), "required": schema{"const": true}}, "state", "required"),
		"system_metadata": object(schema{"state": enum("not_detected"), "source": enum("app_only")}, "state", "source"),
	}, "app", "browser", "system_metadata")
	props["system_metadata"] = object(schema{"languages": schema{"type": "null"}, "region": schema{"type": "null"}, "timezone": schema{"type": "null"}, "reason": enum("app_metadata_not_available_over_rpc")}, "languages", "region", "timezone", "reason")
	return out
}
func measurementResultSchema() schema {
	measurement := schema{"anyOf": []any{messageSchema(&pb.RouteTestSummary{}), messageSchema(&pb.RouteExitSummary{}), messageSchema(&pb.DownloadTestSummary{})}}
	return object(schema{"items": array(object(schema{"route_id": idSchema(), "config_revision": countSchema(), "success": boolean(), "measurement": measurement, "error": messageSchema(&pb.ProductError{})}, "route_id", "config_revision", "success"), 1000)}, "items")
}
func networkDataSchema(name string) schema {
	switch name {
	case "pax_operation_cancel":
		return object(schema{"operation": operationSchema(), "cancel_requested": boolean()}, "operation", "cancel_requested")
	case "pax_physical_egress":
		return messageSchema(&pb.PhysicalEgress{})
	case "pax_traffic_path":
		return messageSchema(&pb.TrafficPathDiagnostics{})
	case "pax_environment_read":
		return environmentSchema()
	case "pax_environment_start":
		return object(schema{"operation": operationSchema(), "session_id": idSchema(), "browser_url": str(512), "expires_at": timeSchema(), "browser_required": schema{"const": true}}, "operation", "session_id", "browser_url", "expires_at", "browser_required")
	default:
		return operationSchema()
	}
}

func proxyResultSchema() schema {
	return object(schema{"status": fieldSchema((&pb.ProxyOperationResult{}).ProtoReflect().Descriptor().Fields().ByName("status")), "selected_route_id": idSchema(), "running_route_id": idSchema(), "degraded_reasons": array(messageSchema(&pb.ProductError{}), 100)}, "status", "degraded_reasons")
}
