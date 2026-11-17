package mcpserver

import (
	pb "pax-connect/daemon/gen/proto/paxv1"
	"strconv"
	"strings"
)

// Explicit projection keeps the public operation contract independent of proto.
func operationData(op *pb.OperationState) map[string]any {
	out := map[string]any{"daemon_instance": op.DaemonInstance, "state": op.State, "cancellable": op.Cancellable}
	for key, value := range map[string]string{"operation_id": op.OperationId, "request_id": op.RequestId, "owner_client_id": op.OwnerClientId, "kind": op.Kind, "unknown_reason": op.UnknownReason} {
		if value != "" {
			out[key] = value
		}
	}
	if op.StartedAt != nil {
		out["started_at"] = op.StartedAt.AsTime().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	if op.FinishedAt != nil {
		out["finished_at"] = op.FinishedAt.AsTime().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	if op.Error != nil {
		code := safeRouteCode(op.Error.Code)
		failure := map[string]any{"code": code, "source": "daemon", "retry": retryCode(code)}
		if len(op.Error.MessageParams) > 0 && (strings.HasSuffix(code, "stale") || strings.HasSuffix(code, "conflict")) {
			if value, err := strconv.ParseUint(op.Error.MessageParams[0], 10, 64); err == nil {
				failure["latest_revision"] = revisionText(value)
			}
		}
		if code == "core_required" {
			failure["details"] = map[string]any{"required_action": "open_pax_and_enable_proxy", "authorization_owner": "app"}
		}
		out["error"] = failure
	}
	if op.AffectedTarget != "" {
		out["affected_target"] = op.AffectedTarget
	}
	if op.Progress != nil {
		progress := map[string]any{"phase": op.Progress.Phase}
		if op.Progress.Completed != nil {
			progress["completed"] = revisionText(*op.Progress.Completed)
		}
		if op.Progress.Total != nil {
			progress["total"] = revisionText(*op.Progress.Total)
		}
		out["progress"] = progress
	}
	if result := settingsResult(op.GetSettings()); result != nil {
		out["result"] = result
	}
	if result := cfOperationResult(op); result != nil {
		out["result"] = result
	}
	if result := networkOperationResult(op); result != nil {
		out["result"] = result
	}
	if value := op.GetAiProfile(); value != nil {
		if op.Kind == "profile.models" {
			out["result"] = profileWorkspace(value.GetWorkspace())
		} else {
			out["result"] = profileResult(value)
		}
	}
	if result := op.GetRouteSave(); result != nil {
		value := map[string]any{"catalog_revision": revisionText(result.CatalogRevision)}
		switch op.Kind {
		case "cf.save":
			value["route_id"], value["revision"], value["config_revision"] = result.RouteId, revisionText(result.RouteRevision), revisionText(result.ConfigRevision)
		case "confirm-import":
			value["imported_route_ids"] = nonNilStrings(result.ImportedRouteIds)
		case "route.delete":
			value["route_id"] = result.RouteId
			value["disposition"] = result.Disposition
		case "SaveRouteGroups":
			groups := []any{}
			for _, group := range result.Groups {
				groups = append(groups, map[string]any{"id": group.Id, "name": scrub(group.Name, false), "member_ids": nonNilStrings(group.MemberIds)})
			}
			value["groups"] = groups
		case "confirm-tls-batch":
			value["changed_route_ids"] = nonNilStrings(result.ChangedRouteIds)
		case "subscription.refresh":
			value["subscription_id"] = result.SubscriptionId
			value["source_revision"] = revisionText(result.SourceRevision)
			value["added"] = revisionText(uint64(result.Added))
			value["updated"] = revisionText(uint64(result.Updated))
			value["hidden"] = revisionText(uint64(result.Hidden))
			value["removed"] = revisionText(uint64(result.Removed))
		case "delete-subscription":
			value["subscription_id"] = result.SubscriptionId
			value["deleted_route_ids"] = nonNilStrings(result.DeletedRouteIds)
			value["retained_route_ids"] = nonNilStrings(result.RetainedRouteIds)
		default:
			if result.RouteId != "" {
				value["route_id"] = result.RouteId
			}
			if result.RouteRevision > 0 {
				value["route_revision"] = revisionText(result.RouteRevision)
			}
			if op.Kind == "route.save" || op.Kind == "save-chain" || op.Kind == "add" {
				value["created"] = result.Created
			}
		}
		out["result"] = value
	}
	return out
}
func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
func safeRouteCode(code string) string {
	if settingsErrorCodes[code] || cfErrorCodes[code] || profileErrorCodes[code] || routeErrorCodes[code] || productErrors[code] || networkErrorCodes[code] {
		return code
	}
	return "internal_error"
}

var routeErrorCodes = map[string]bool{}

func init() {
	for _, code := range strings.Fields(`catalog_limit route_not_hidden chain_udp_required chain_name_invalid subscription_duplicate subscription_match_ambiguous fetch_scheme_unsupported fetch_target_disallowed route_invalid route_stale route_not_found route_referenced route_in_use route_save_failed route_prepare_timeout route_not_selected route_same route_hidden route_source_removed catalog_invalid catalog_unavailable catalog_stale request_invalid request_conflict request_expired daemon_restarted credentials_required secret_field_requires_update secret_update_invalid redaction_unsupported tls_confirmation_required tls_identity_changed tls_policy_stale tls_policy_not_applicable tls_policy_store_invalid tls_policy_save_failed tls_batch_invalid tls_batch_unavailable unsupported_protocol unsupported_transport unsupported_field chain_invalid chain_edit_required chain_same chain_nested group_invalid group_limit group_name_invalid group_member_invalid subscription_not_found subscription_stale subscription_unavailable subscription_invalid subscription_refresh_busy subscription_saved_route_unavailable subscription_proxy_unavailable subscription_in_use subscription_referenced subscription_replacement_incomplete subscription_format_unsupported import_too_large empty_import empty_import_selection invalid_preview_item import_item_invalid preview_expired preview_stale preview_limit preview_owner_mismatch operation_busy operation_cancelled fetch_failed fetch_forbidden fetch_dns_failed fetch_tls_failed fetch_timeout fetch_http_failed fetch_connection_failed fetch_scheme_invalid fetch_target_invalid fetch_redirect_limit fetch_too_large fetch_url_invalid`) {
		routeErrorCodes[code] = true
	}
}
