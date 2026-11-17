package mcpserver

import (
	"context"
	"strings"
	"testing"
)

func TestToolPermissionsMatchContract(t *testing.T) {
	// Independent expectations from tool-contracts.md and its four tool tables.
	// Do not derive these from the production tool lists or permission functions.
	groups := map[string]string{
		"read": `pax_status pax_routes_list pax_logs_query pax_log_objects pax_log_detail pax_logs_updates
			pax_traffic_query pax_traffic_detail pax_route_history pax_route_usage pax_operation_get
			pax_ai_summary pax_ai_buckets pax_ai_logs pax_ai_sessions pax_ai_analysis pax_route_get
			pax_physical_egress pax_traffic_path pax_environment_read pax_profiles_list pax_cf_workspace pax_settings_get`,
		"manage": `pax_routes_preview pax_routes_preview_cancel pax_routes_import pax_route_add pax_route_update
			pax_route_tls pax_routes_tls_batch pax_route_groups_save pax_route_chain_save
			pax_subscription_refresh pax_subscription_restore_route pax_cf_save`,
		"network": `pax_proxy_set pax_route_select`,
		"diagnose": `pax_routes_test pax_path_recheck pax_connection_diagnose pax_network_diagnose
			pax_environment_start pax_environment_sources_retry pax_cf_run pax_cf_validate`,
		"profiles": `pax_profile_save pax_profile_import pax_profile_models_refresh pax_profile_plan pax_profile_apply`,
		"settings": `pax_privacy_save pax_measurement_save pax_measurement_reset pax_logs_retention`,
		"delete":   `pax_route_delete pax_subscription_delete pax_profile_delete pax_logs_clear`,
	}
	for _, allow := range []string{"read", "manage", "network", "diagnose", "profiles", "settings", "delete", "read,manage,network,diagnose,profiles,settings,delete"} {
		t.Run(allow, func(t *testing.T) {
			want := map[string]bool{} // tool -> readOnlyHint
			for _, permission := range strings.Split(allow, ",") {
				for _, name := range strings.Fields(groups[permission]) {
					want[name] = permission == "read"
				}
				if permission != "read" {
					want["pax_operation_cancel"] = false
				}
			}
			s, err := New(Options{Allow: allow})
			if err != nil {
				t.Fatal(err)
			}
			s.open = func(context.Context) (*backend, error) {
				t.Fatal("tool discovery contacted daemon")
				return nil, nil
			}
			list, err := sdkClient(t, s).ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, tool := range list.Tools {
				readonly, ok := want[tool.Name]
				if !ok {
					t.Errorf("unexpected tool %s", tool.Name)
				} else if tool.Annotations == nil || tool.Annotations.ReadOnlyHint != readonly {
					t.Errorf("%s: readOnlyHint must be %v", tool.Name, readonly)
				}
				delete(want, tool.Name)
			}
			for name := range want {
				t.Errorf("missing tool %s", name)
			}
		})
	}
}
