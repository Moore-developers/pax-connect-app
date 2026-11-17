package mcpserver

import pb "pax-connect/daemon/gen/proto/paxv1"

var profileTools = []string{"pax_profiles_list", "pax_profile_save", "pax_profile_import", "pax_profile_delete", "pax_profile_models_refresh", "pax_profile_plan", "pax_profile_apply"}

func isProfileTool(name string) bool { return slicesContain(profileTools, name) }
func profilePermission(name string) string {
	if name == "pax_profiles_list" {
		return "read"
	}
	if name == "pax_profile_delete" {
		return "delete"
	}
	return "profiles"
}
func profileDescription(name string) string {
	switch name {
	case "pax_profiles_list":
		return "Read secret-free saved Profiles and observed global CLI files. DSH is usage-only. Does not fetch provider models."
	case "pax_profile_save":
		return "Save API Profile metadata and a keep/replace credential, without changing CLI files. New IDs are client-generated UUIDs; edits require expected_revision. New credentials pass through your MCP client and AI service. Native profiles allow existing metadata edits only. Provider directory discovery is separate from the saved result."
	case "pax_profile_import":
		return "Import the currently inspected Claude/Codex API credentials directly inside Pax under a stable UUID. Never returns the key or changes CLI files. DSH and account conversion are unsupported."
	case "pax_profile_delete":
		return "Delete an unused saved Profile at its exact revision. Bound profiles cannot be deleted; usage history is retained."
	case "pax_profile_models_refresh":
		return "Start a bounded read of a saved provider's model directory; no generation request. The daemon retains its result across MCP disconnects. modelsState/modelsError describe discovery failure separately from saved credentials."
	case "pax_profile_plan":
		return "Preview a provider-only Claude/Codex configuration switch against the inspection and Profile revision. Plan expires in 60 seconds; a new plan for the same CLI invalidates the old. initialize explicitly creates first API configuration and never converts account login. No restore or DSH writes."
	default:
		return "Apply an unexpired plan after rechecking CLI files and Profile revision. Preserves MCP and unrelated fields. Conflicting external edits are rejected. Writes disk configuration only; a new CLI session may be needed. Query the original request after timeout or disconnect."
	}
}
func profileIDSchema() schema {
	out := str(36)
	out["pattern"] = "^(?:[a-fA-F0-9]{32}|[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12})$"
	return out
}
func multiplierSchema() schema {
	return schema{"type": "number", "minimum": 0, "maximum": 100, "multipleOf": 0.001}
}
func modelMultipliersSchema() schema {
	return schema{"type": "object", "maxProperties": 1000, "propertyNames": schema{"type": "string", "minLength": 1, "maxLength": 200}, "additionalProperties": multiplierSchema()}
}
func profileInputSchema(name string) schema {
	props := schema{}
	required := []string{}
	switch name {
	case "pax_profiles_list":
		props = pageProps()
	case "pax_profile_save":
		metadata := schema{"id": profileIDSchema(), "name": nameSchema(), "multiplier": multiplierSchema(), "model_multipliers": modelMultipliersSchema()}
		api := object(merge(merge(schema{}, metadata), schema{"base_url": str(8192), "model_ids": array(str(200), 1000)}), "id", "name", "base_url", "multiplier", "model_ids")
		key := object(schema{"mode": enum("keep", "replace"), "api_key": str(16 << 10)}, "mode")
		key["oneOf"] = []any{schema{"properties": schema{"mode": enum("keep")}, "not": schema{"required": []string{"api_key"}}}, schema{"properties": schema{"mode": enum("replace"), "api_key": schema{"minLength": 1}}, "required": []string{"api_key"}}}
		props = schema{"profile": api, "native_profile": object(metadata, "id", "name", "multiplier"), "expected_revision": revisionSchema(), "key_change": key}
	case "pax_profile_import":
		props = schema{"cli": str(32), "inspection_id": idSchema(), "id": profileIDSchema(), "name": nameSchema(), "multiplier": multiplierSchema()}
		required = []string{"cli", "inspection_id", "id", "name", "multiplier"}
	case "pax_profile_delete":
		props = schema{"profile_id": profileIDSchema(), "expected_revision": revisionSchema()}
		required = []string{"profile_id", "expected_revision"}
	case "pax_profile_models_refresh":
		props = schema{"profile_id": profileIDSchema()}
		required = []string{"profile_id"}
	case "pax_profile_plan":
		props = schema{"cli": str(32), "profile_id": profileIDSchema(), "revision": revisionSchema(), "inspection_id": idSchema(), "initialize": boolean(), "model_id": str(200)}
		required = []string{"cli", "profile_id", "revision", "inspection_id"}
	case "pax_profile_apply":
		props = schema{"plan_id": idSchema()}
		required = []string{"plan_id"}
	}
	if name != "pax_profiles_list" {
		props["request_id"] = routeInputSchema("pax_routes_preview_cancel")["properties"].(schema)["request_id"]
		props["daemon_instance"] = idSchema()
		required = append(required, "request_id", "daemon_instance")
	}
	out := object(props, required...)
	out["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	if name == "pax_profile_save" {
		out["oneOf"] = []any{schema{"required": []string{"profile", "key_change"}, "not": schema{"required": []string{"native_profile"}}}, schema{"required": []string{"native_profile", "expected_revision"}, "not": schema{"anyOf": []any{schema{"required": []string{"profile"}}, schema{"required": []string{"key_change"}}}}}}
	}
	return out
}
func profileWorkspaceSchema() schema {
	out := messageSchema(&pb.AIProfilesWorkspace{})
	bindings := out["properties"].(schema)["bindings"].(schema)["items"].(schema)["properties"].(schema)
	delete(bindings, "templateRevision")
	return out
}
func profileResultSchema() schema {
	return object(schema{"workspace": profileWorkspaceSchema(), "plan": messageSchema(&pb.AIProfilePlan{}), "profile_id": profileIDSchema(), "deleted_profile_id": profileIDSchema(), "outcome": enum("applied", "rolled_back", "recovery_required"), "operation_id": idSchema()})
}
func profileDataSchema(name string) schema {
	switch name {
	case "pax_profiles_list":
		return profileWorkspaceSchema()
	case "pax_profile_models_refresh":
		return operationSchema()
	case "pax_profile_plan":
		return messageSchema(&pb.AIProfilePlan{})
	default:
		return profileResultSchema()
	}
}
