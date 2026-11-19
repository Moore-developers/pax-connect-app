package mcpserver

import (
	_ "embed"
	"encoding/json"
	pb "pax-connect/daemon/gen/proto/paxv1"
)

// Pinned sing-box v1.14.3 schema intersected with the product contract; regenerate
// using scripts/generate-mcp-document-schema.py. No upstream Go types cross here.
//
//go:embed schema/document.json
var documentSchemaJSON []byte

var routeTools = []string{"pax_route_get", "pax_routes_preview", "pax_routes_preview_cancel", "pax_routes_import", "pax_route_add", "pax_route_update", "pax_route_delete", "pax_route_tls", "pax_routes_tls_batch", "pax_route_groups_save", "pax_route_chain_save", "pax_subscription_refresh", "pax_subscription_delete", "pax_subscription_restore_route"}

func routePermission(name string) string {
	switch name {
	case "pax_route_get":
		return "read"
	case "pax_route_delete", "pax_subscription_delete":
		return "delete"
	default:
		return "manage"
	}
}
func routeDescription(name string) string {
	switch name {
	case "pax_route_get":
		return "Read one route's editable document with all credentials removed at the daemon; presence only. Chains return member references."
	case "pax_routes_preview":
		return "Preview share links, Base64 subscriptions, Clash YAML, sing-box JSON, or a controlled HTTPS subscription. Review invalid, duplicate, or hidden items before saving. Use a stable client ID. Tokens expire in five minutes."
	case "pax_routes_preview_cancel":
		return "Release this client's pending import preview. Does not remove saved routes."
	case "pax_routes_import":
		return "Save only selected valid preview items at the expected catalog revision. Never downloads again or enables proxy."
	case "pax_route_add", "pax_route_update":
		return "Save one supported outbound's complete non-secret JSON. Omitted non-secret fields are removed; omitted secrets are retained only from the checked version and protocol. secret_updates are write-only: new values pass through the MCP client and AI service, so avoid providing existing keys in model context. TLS skip requires the dedicated TLS action."
	case "pax_route_delete":
		return "Permanently delete a local route or hide a subscription route. Saved references/running use can prevent deletion. Historical traffic is retained."
	case "pax_route_tls":
		return "Save the next connection's TLS policy for the exact route revision and TLS identity; skip_verify requires acknowledge_skip. Does not reconnect."
	case "pax_routes_tls_batch":
		return "Atomically confirm skip_verify only for exact identities with current measured certificate failures. Existing business rules reject strict mode in this batch; use pax_route_tls for strict."
	case "pax_route_groups_save":
		return "Replace all custom route groups at the expected catalog revision. Removing groups never deletes routes."
	case "pax_route_chain_save":
		return "Create or edit a two-hop chain referencing two existing ordinary routes at their exact revisions. Nested or duplicate members are rejected."
	case "pax_subscription_refresh":
		return "Refresh a saved controlled HTTPS subscription via system, frozen saved route or last channel. Persist replacement URL only after successful parsing. Does not switch the active proxy."
	case "pax_subscription_delete":
		return "Permanently delete a subscription and its allowed saved/hidden routes and overrides. Active routes or saved references prevent this atomic deletion; historical traffic is retained."
	default:
		return "Restore a hidden subscription route at its exact revision. Does not select or connect it."
	}
}
func revisionSchema() schema { return schema{"type": "string", "pattern": "^[1-9][0-9]{0,19}$"} }
func nameSchema() schema     { out := str(200); out["minLength"] = 1; return out }
func refs() schema           { return schema{"route_id": idSchema(), "expected_revision": revisionSchema()} }
func nonemptyArray(item schema, max int) schema {
	out := array(item, max)
	out["minItems"] = 1
	return out
}
func groupSchema() schema {
	return object(schema{"id": idSchema(), "name": nameSchema(), "member_ids": array(idSchema(), 1000)}, "id", "name", "member_ids")
}
func tlsChangeSchema() schema {
	return object(merge(refs(), schema{"identity_token": str(8192), "mode": enum("strict", "skip_verify"), "acknowledge_skip": boolean()}), "route_id", "expected_revision", "identity_token", "mode")
}
func documentSchema() schema { return schema{"$ref": "#/$defs/Outbound"} }
func addDocumentDefinitions(out schema) schema {
	var defs schema
	_ = json.Unmarshal(documentSchemaJSON, &defs)
	out["$defs"] = defs
	return out
}
func routeInputSchema(name string) schema {
	props := schema{}
	required := []string{}
	switch name {
	case "pax_route_get":
		props = schema{"route_id": idSchema()}
		required = []string{"route_id"}
	case "pax_routes_preview":
		props = merge(schema{"source": str(2 << 20), "is_subscription": boolean(), "source_name": nameSchema()}, pageProps())
		props["source"].(schema)["minLength"] = 1
		required = []string{"source"}
	case "pax_routes_preview_cancel":
		props = schema{"token": str(8192)}
		required = []string{"token"}
	case "pax_routes_import":
		props = schema{"token": str(8192), "item_ids": nonemptyArray(idSchema(), 1000), "expected_catalog_revision": revisionSchema()}
		required = []string{"token", "item_ids", "expected_catalog_revision"}
	case "pax_route_add", "pax_route_update":
		change := object(schema{"path": str(512), "value": str(16 << 10), "clear": schema{"const": true}}, "path")
		change["oneOf"] = []any{schema{"required": []string{"value"}, "not": schema{"required": []string{"clear"}}}, schema{"required": []string{"clear"}, "not": schema{"required": []string{"value"}}}}
		props = schema{"name": nameSchema(), "document": documentSchema(), "secret_updates": array(change, 32)}
		required = []string{"name", "document"}
		if name == "pax_route_update" {
			merge(props, refs())
			required = append(required, "route_id", "expected_revision")
		}
	case "pax_route_delete", "pax_subscription_restore_route":
		props = refs()
		required = []string{"route_id", "expected_revision"}
	case "pax_route_tls":
		props = tlsChangeSchema()["properties"].(schema)
		required = []string{"route_id", "expected_revision", "identity_token", "mode"}
	case "pax_routes_tls_batch":
		props = schema{"routes": nonemptyArray(tlsChangeSchema(), 100)}
		required = []string{"routes"}
	case "pax_route_groups_save":
		props = schema{"groups": array(groupSchema(), 1000), "expected_catalog_revision": revisionSchema()}
		required = []string{"groups", "expected_catalog_revision"}
	case "pax_route_chain_save":
		props = schema{"id": idSchema(), "name": nameSchema(), "front_id": idSchema(), "final_id": idSchema(), "expected_revision": revisionSchema(), "front_revision": revisionSchema(), "final_revision": revisionSchema()}
		required = []string{"name", "front_id", "final_id", "front_revision", "final_revision"}
	case "pax_subscription_refresh":
		route := object(merge(refs(), schema{"expected_config_revision": revisionSchema()}), "route_id", "expected_revision", "expected_config_revision")
		props = schema{"subscription_id": idSchema(), "expected_source_revision": revisionSchema(), "channel": enum("system", "route", "last"), "route": route, "replacement_url": str(8192), "user_agent": str(512)}
		required = []string{"subscription_id", "expected_source_revision", "channel"}
	case "pax_subscription_delete":
		props = schema{"subscription_id": idSchema(), "expected_source_revision": revisionSchema(), "expected_catalog_revision": revisionSchema()}
		required = []string{"subscription_id", "expected_source_revision", "expected_catalog_revision"}
	}
	if name != "pax_route_get" {
		props["request_id"] = str(128)
		props["request_id"].(schema)["pattern"] = "^[0-9]+:[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$"
		props["daemon_instance"] = idSchema()
		required = append(required, "request_id", "daemon_instance")
	}
	out := object(props, required...)
	out["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	if name == "pax_route_add" || name == "pax_route_update" {
		addDocumentDefinitions(out)
	}
	if name == "pax_route_chain_save" {
		out["oneOf"] = []any{schema{"required": []string{"id", "expected_revision"}}, schema{"not": schema{"anyOf": []any{schema{"required": []string{"id"}}, schema{"required": []string{"expected_revision"}}}}}}
	}
	if name == "pax_subscription_refresh" {
		out["oneOf"] = []any{schema{"properties": schema{"channel": enum("route")}, "required": []string{"route"}}, schema{"properties": schema{"channel": enum("system", "last")}, "not": schema{"required": []string{"route"}}}}
	}
	return out
}
func routeDataSchema(name string) schema {
	switch name {
	case "pax_route_get":
		return object(schema{"route_id": idSchema(), "revision": revisionSchema(), "config_revision": revisionSchema(), "name": str(200), "document": documentSchema(), "credentials": array(object(schema{"path": str(512), "present": boolean()}, "path", "present"), 1000), "hidden": boolean(), "source_removed": boolean(), "chain": object(schema{"front_id": idSchema(), "final_id": idSchema()}, "front_id", "final_id")}, "route_id", "revision", "config_revision", "name", "hidden", "source_removed")
	case "pax_routes_preview":
		return messageSchema(&pb.RoutesPreview{})
	case "pax_routes_preview_cancel":
		return object(schema{"released": boolean()}, "released")
	default:
		return operationSchema()
	}
}
func operationSchema() schema {
	result := object(schema{"revision": revisionSchema(), "config_revision": revisionSchema(), "route_id": idSchema(), "created": boolean(), "catalog_revision": revisionSchema(), "route_revision": revisionSchema(), "disposition": enum("deleted", "hidden"), "imported_route_ids": array(idSchema(), 1000), "changed_route_ids": array(idSchema(), 100), "groups": array(groupSchema(), 1000), "subscription_id": idSchema(), "source_revision": revisionSchema(), "added": countSchema(), "updated": countSchema(), "hidden": countSchema(), "removed": countSchema(), "deleted_route_ids": array(idSchema(), 1000), "retained_route_ids": array(idSchema(), 1000)})
	return object(schema{"operation_id": idSchema(), "daemon_instance": idSchema(), "request_id": str(128), "owner_client_id": idSchema(), "kind": str(128), "state": enum("accepted", "running", "succeeded", "failed", "cancelled", "unknown"), "cancellable": boolean(), "started_at": timeSchema(), "finished_at": timeSchema(), "unknown_reason": enum("expired", "daemon_restarted", "not_found", "delivery_uncertain"), "result": schema{"anyOf": []any{privacySchema(), preferencesSchema(), retentionResultSchema(), clearResultSchema(), result, cfValidationSchema(), messageSchema(&pb.CFWorkspace{}), profileResultSchema(), profileWorkspaceSchema(), proxyResultSchema(), measurementResultSchema(), messageSchema(&pb.CurrentPathCheck{}), messageSchema(&pb.TrafficPathDiagnostics{}), messageSchema(&pb.NetworkDiagnosticReport{}), environmentSchema()}}, "affected_target": idSchema(), "progress": object(schema{"phase": str(128), "completed": countSchema(), "total": countSchema()}, "phase"), "error": object(schema{"code": str(80), "source": str(128), "retry": enum("read", "same_request", "replan", "never"), "latest_revision": countSchema(), "details": object(schema{"required_action": str(128), "authorization_owner": str(128)})})}, "daemon_instance", "state", "cancellable")
}
