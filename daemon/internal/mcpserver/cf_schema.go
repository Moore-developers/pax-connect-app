package mcpserver

import pb "pax-connect/daemon/gen/proto/paxv1"

var cfTools = []string{"pax_cf_workspace", "pax_cf_run", "pax_cf_validate", "pax_cf_save"}

func isCFTool(name string) bool {
	for _, v := range cfTools {
		if name == v {
			return true
		}
	}
	return false
}
func cfPermission(name string) string {
	switch name {
	case "pax_cf_workspace":
		return "read"
	case "pax_cf_save":
		return "manage"
	default:
		return "diagnose"
	}
}
func cfDescription(name string) string {
	switch name {
	case "pax_cf_workspace":
		return "Read CF workspace and seed candidates without probing. TCP reachability, edge download and complete-route validation are separate facts. Results are paginated and keep their network/timestamps."
	case "pax_cf_run":
		return "Start an explicit CF scan/check/download/location task on the physical network. Uses existing fixed targets, concurrency and bounded download budgets (8 MiB per IP). Poll/cancel with operation tools. Never changes a route. Terminal result contains recordCount/recordsOmitted; read all rows with pax_cf_workspace."
	case "pax_cf_validate":
		return "Validate the complete route/chain through a CF IP for up to 12 seconds. Optional non-secret document preserves credentials after CAS; secret_updates are write-only. Returns a private frozen-draft token valid up to 5 minutes on the same network and versions. Does not save/select/enable a route."
	default:
		return "Explicitly save exactly the successfully validated frozen draft and CF IP. Requires manage permission, current route/config/chain/network and unexpired token. No new probe, selection or proxy activation."
	}
}
func cfTargetSchema() schema {
	return object(schema{"route_id": idSchema(), "expected_route_revision": revisionSchema(), "expected_config_revision": revisionSchema()}, "route_id", "expected_route_revision", "expected_config_revision")
}
func cfInputSchema(name string) schema {
	props := schema{}
	required := []string{}
	switch name {
	case "pax_cf_workspace":
		props = merge(schema{"network": idSchema(), "route": cfTargetSchema()}, pageProps())
	case "pax_cf_run":
		props = schema{"action": enum("scan", "download", "check", "locate", "latency_one", "download_one", "measure_one"), "ips": nonemptyArray(str(64), 100)}
		required = []string{"action"}
	case "pax_cf_validate", "pax_cf_save":
		props = cfTargetSchema()["properties"].(schema)
		required = []string{"route_id", "expected_route_revision", "expected_config_revision"}
		if name == "pax_cf_validate" {
			updates := routeInputSchema("pax_route_add")["properties"].(schema)["secret_updates"]
			props["ip"], props["document"], props["secret_updates"], props["chain"] = str(64), documentSchema(), updates, object(schema{"id": idSchema(), "revision": revisionSchema()}, "id", "revision")
			required = append(required, "ip")
		} else {
			props["validation_id"] = str(8192)
			props["validation_id"].(schema)["minLength"] = 1
			required = append(required, "validation_id")
		}
	}
	if name != "pax_cf_workspace" {
		identity := routeInputSchema("pax_routes_preview_cancel")["properties"].(schema)
		props["request_id"], props["daemon_instance"] = identity["request_id"], identity["daemon_instance"]
		required = append(required, "request_id", "daemon_instance")
	}
	out := object(props, required...)
	out["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	if name == "pax_cf_validate" {
		addDocumentDefinitions(out)
		out["dependentRequired"] = schema{"secret_updates": []string{"document"}}
	}
	return out
}
func cfValidationSchema() schema {
	return object(schema{"candidate": messageSchema(&pb.CFCandidate{}), "validation_id": str(8192), "draft_digest": idSchema(), "network": idSchema(), "route_revision": revisionSchema(), "config_revision": revisionSchema(), "expires_at": timeSchema(), "success": boolean()})
}
func cfDataSchema(name string) schema {
	if name == "pax_cf_workspace" {
		return object(schema{"workspace": messageSchema(&pb.CFWorkspace{}), "candidates": messageSchema(&pb.CFCandidates{}), "workspace_at": timeSchema(), "candidates_at": timeSchema()}, "workspace", "candidates", "workspace_at", "candidates_at")
	}
	return operationSchema()
}
