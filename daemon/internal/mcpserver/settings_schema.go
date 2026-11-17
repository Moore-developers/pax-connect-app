package mcpserver

import pb "pax-connect/daemon/gen/proto/paxv1"

var settingsTools = []string{"pax_settings_get", "pax_privacy_save", "pax_measurement_save", "pax_measurement_reset", "pax_logs_retention", "pax_logs_clear"}

func isSettingsTool(name string) bool { return slicesContain(settingsTools, name) }
func settingsPermission(name string) string {
	if name == "pax_settings_get" {
		return "read"
	}
	if name == "pax_logs_clear" {
		return "delete"
	}
	return "settings"
}
func settingsDescription(name string) string {
	switch name {
	case "pax_settings_get":
		return "Read all formal network privacy, route measurement and log settings, with independent owner revisions. No probes. Privacy takes effect on the next explicit proxy activation; measurement changes affect the next batch except pending exit requests; log settings apply immediately."
	case "pax_privacy_save":
		return "Save DNS balanced/strict and WebRTC protection at expected_revision. Takes effect on the next explicit proxy activation; does not reconnect."
	case "pax_measurement_save":
		return "Save independent latency, exit and download concurrency modes at expected_revision. Manual mode requires parallel. Default/auto reject parallel; daemon supplies actual values. Exit auto remains eight. No new measurement."
	case "pax_measurement_reset":
		return "Restore only route measurement defaults at expected_revision. Hardware latency default and exit/download eight come from the daemon; privacy and logs are retained."
	case "pax_logs_retention":
		return "Set log retention to 1, 12 or 24 hours at expected_revision. Shortening also requires delete permission and deletes expired logs, raw text and connection details, invalidating affected read tokens. Preserves traffic totals and current connections. Query the returned operation for the actual cleanup result."
	default:
		return "Delete all product logs and raw text. Preserves traffic statistics, current connections and AI request history. Daemon generates the clear time and operation ID. ACK is acceptance only; query the operation for committed generations."
	}
}
func settingsInputSchema(name string) schema {
	props, required := schema{}, []string{}
	switch name {
	case "pax_privacy_save":
		props = schema{"expected_revision": revisionSchema(), "dns_mode": enum("balanced", "strict"), "webrtc": boolean()}
		required = []string{"expected_revision", "dns_mode", "webrtc"}
	case "pax_measurement_save":
		mode := func(max int) schema {
			return schema{"oneOf": []any{object(schema{"mode": enum("default", "auto")}, "mode"), object(schema{"mode": enum("manual"), "parallel": integer(1, max)}, "mode", "parallel")}}
		}
		props = schema{"expected_revision": revisionSchema(), "latency": mode(100), "exit": mode(32), "download": mode(8)}
		required = []string{"expected_revision", "latency", "exit", "download"}
	case "pax_measurement_reset":
		props["expected_revision"] = revisionSchema()
		required = []string{"expected_revision"}
	case "pax_logs_retention":
		props = schema{"expected_revision": countSchema(), "hours": schema{"type": "integer", "enum": []int{1, 12, 24}}}
		required = []string{"expected_revision", "hours"}
	}
	if name != "pax_settings_get" {
		props["request_id"] = routeInputSchema("pax_routes_preview_cancel")["properties"].(schema)["request_id"]
		props["daemon_instance"] = idSchema()
		required = append(required, "request_id", "daemon_instance")
	}
	out := object(props, required...)
	out["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	return out
}
func privacySchema() schema {
	out := messageSchema(&pb.NetworkPrivacy{})
	delete(out["properties"].(schema), "context")
	return out
}
func preferencesSchema() schema {
	out := messageSchema(&pb.MeasurementPreferences{})
	delete(out["properties"].(schema), "context")
	return out
}
func retentionResultSchema() schema {
	return object(schema{"revision": countSchema(), "retention_hours": integer(1, 24), "cleanup": object(schema{"state": enum("scheduled", "completed"), "scope": enum("logs_and_connection_details")}, "state", "scope")}, "revision", "retention_hours", "cleanup")
}
func clearResultSchema() schema {
	return object(schema{"scope": enum("logs_and_raw"), "requested_at": timeSchema(), "clear_generation": countSchema(), "logs_revision": revisionSchema()}, "scope", "requested_at", "clear_generation", "logs_revision")
}
func settingsDataSchema(name string) schema {
	switch name {
	case "pax_settings_get":
		return object(schema{"network_privacy": privacySchema(), "measurement": preferencesSchema(), "logs": object(schema{"revision": countSchema(), "retention_hours": integer(1, 24)}, "revision", "retention_hours"), "effective": object(schema{"privacy": enum("next_proxy_activation"), "measurement": enum("next_batch_except_exit_pending"), "logs": enum("immediate")}, "privacy", "measurement", "logs")}, "network_privacy", "measurement", "logs", "effective")
	case "pax_privacy_save":
		return privacySchema()
	case "pax_measurement_save", "pax_measurement_reset":
		return preferencesSchema()
	default:
		return operationSchema()
	}
}
