package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
)

func stringArg(args map[string]any, key, fallback string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return fallback
}
func intArg(args map[string]any, key string, fallback int) int {
	if v, ok := args[key].(float64); ok {
		return int(v)
	}
	if v, ok := args[key].(int); ok {
		return v
	}
	return fallback
}
func boolArg(args map[string]any, key string) bool { v, _ := args[key].(bool); return v }
func countArg(args map[string]any, key string) (uint64, error) {
	v, err := strconv.ParseUint(stringArg(args, key, "0"), 10, 64)
	if err != nil {
		return 0, errors.New("invalid_argument")
	}
	return v, nil
}

func queryRange(args map[string]any, span, max time.Duration) (time.Time, time.Time, error) {
	if zone := stringArg(args, "timezone", ""); zone != "" {
		if zone == "Local" {
			return time.Time{}, time.Time{}, errors.New("invalid_range")
		}
		if _, err := time.LoadLocation(zone); err != nil {
			return time.Time{}, time.Time{}, errors.New("invalid_range")
		}
	}
	value, ok := args["range"].(map[string]any)
	if !ok {
		until := time.Now().UTC()
		from := until.Add(-span)
		args["range"] = map[string]any{"from": from.Format(time.RFC3339Nano), "until": until.Format(time.RFC3339Nano)}
		return from, until, nil
	}
	from, err := time.Parse(time.RFC3339Nano, stringArg(value, "from", ""))
	if err != nil {
		return from, from, errors.New("invalid_range")
	}
	until, err := time.Parse(time.RFC3339Nano, stringArg(value, "until", ""))
	if err != nil || !from.Before(until) || until.Sub(from) > max {
		return from, until, errors.New("invalid_range")
	}
	return from, until, nil
}

func logQuery(args map[string]any) (*pb.LogQuery, error) {
	from, until, err := queryRange(args, time.Hour, 24*time.Hour)
	if err != nil {
		return nil, err
	}
	out := &pb.LogQuery{View: stringArg(args, "view", "activity"), Dimension: stringArg(args, "dimension", "application"), ObjectId: stringArg(args, "object_id", ""), Search: stringArg(args, "search", ""), Level: stringArg(args, "level", ""), RouteId: stringArg(args, "route_id", ""), From: timestamppb.New(from), Until: timestamppb.New(until), IncludeDns: boolArg(args, "include_dns"), Scope: stringArg(args, "scope", ""), Quick: stringArg(args, "quick", "all"), ProblemId: stringArg(args, "problem_id", ""), ConnectionKey: stringArg(args, "connection_key", "")}
	// The native log contract is (from, until]; MCP exposes [from, until).
	out.From = timestamppb.New(from.Add(-time.Nanosecond))
	out.Until = timestamppb.New(until.Add(-time.Nanosecond))
	if out.Dimension == "system" && out.Scope != "system" {
		return nil, errors.New("invalid_argument")
	}
	if value, ok := args["criteria"]; ok {
		raw, _ := json.Marshal(value)
		out.Criteria = &pb.LogCriteria{}
		if err = protojson.Unmarshal(raw, out.Criteria); err != nil {
			return nil, errors.New("invalid_argument")
		}
	}
	return out, nil
}

func (s *Server) query(ctx context.Context, b *backend, p *queryPage) (map[string]any, string, string, error) {
	if p.tool == "pax_cf_workspace" {
		return s.cfQuery(ctx, b, p)
	}
	if p.tool == "pax_profiles_list" {
		return s.profilesQuery(ctx, b)
	}
	if isAITool(p.tool) {
		return s.queryAI(ctx, b, p)
	}
	args := p.args
	var message proto.Message
	var err error
	cursor, readToken := "", ""
	switch p.tool {
	case "pax_status":
		snap, e := b.rpc.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
		if e != nil {
			return nil, "", "", e
		}
		public, e := projection(snap)
		if e != nil {
			return nil, "", "", e
		}
		out := map[string]any{"daemon_version": b.hello.DaemonVersion, "protocol_version": b.hello.ProtocolVersion, "capabilities": b.hello.Capabilities, "owner_app_pid": b.hello.OwnerAppPid, "protection_status": snap.Status.String(), "selected_route_id": snap.SelectedRouteId, "pending_operation_id": snap.PendingOperationId, "degraded_reasons": public["degradedReasons"], "today_traffic": public["todayTraffic"]}
		if snap.CurrentRoute != nil {
			out["running_route_id"] = snap.CurrentRoute.Id
		}
		for _, pair := range [][2]string{{"currentPathCheck", "current_path_check"}, {"history", "history"}, {"aiUsage", "ai_usage"}} {
			if v, ok := public[pair[0]]; ok {
				out[pair[1]] = v
			}
		}
		if snap.TodayTraffic == nil {
			delete(out, "today_traffic")
		}
		return out, "", "", nil
	case "pax_routes_list":
		snap, e := b.rpc.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
		if e != nil {
			return nil, "", "", e
		}
		public, e := projection(snap)
		if e != nil {
			return nil, "", "", e
		}
		members := map[string]bool{}
		groupID := stringArg(args, "group_id", "")
		for _, group := range snap.RouteGroups {
			if group.Id == groupID {
				for _, id := range group.MemberIds {
					members[id] = true
				}
			}
		}
		filtered := []any{}
		all, _ := public["routes"].([]any)
		for i, route := range snap.Routes {
			if route.Hidden && !boolArg(args, "include_hidden") {
				continue
			}
			if source := stringArg(args, "subscription_id", ""); source != "" && route.SourceId != source {
				continue
			}
			if groupID != "" && !members[route.Id] {
				continue
			}
			if search := stringArg(args, "search", ""); search != "" && !strings.Contains(strings.ToLower(route.Name), strings.ToLower(search)) {
				continue
			}
			filtered = append(filtered, all[i])
		}
		return map[string]any{"catalog_revision": strconv.FormatUint(snap.CatalogRevision, 10), "routes": filtered, "subscriptions": public["subscriptions"], "groups": public["routeGroups"]}, "", "", nil
	case "pax_logs_query", "pax_log_objects":
		if p.tool == "pax_logs_query" && stringArg(args, "search", "") != "" {
			return nil, "", "", errors.New("invalid_filter")
		}
		q, e := logQuery(args)
		if e != nil {
			return nil, "", "", e
		}
		q.Limit = uint32(p.limit)
		q.Cursor = p.cursor
		q.ReadToken = p.readToken
		if p.tool == "pax_logs_query" {
			out, e := b.rpc.QueryLogs(ctx, q)
			message, err = out, e
			if e == nil {
				cursor, readToken = out.NextCursor, out.ReadToken
			}
		} else {
			out, e := b.rpc.QueryLogObjects(ctx, q)
			message, err = out, e
			if e == nil {
				cursor, readToken = out.NextCursor, out.ReadToken
			}
		}
	case "pax_log_detail":
		filter, _ := args["filter"].(map[string]any)
		q, e := logQuery(filter)
		if e != nil {
			return nil, "", "", e
		}
		q.ReadToken = stringArg(args, "read_token", "")
		message, err = b.rpc.GetLogDetail(ctx, &pb.LogDetailRequest{RecordId: stringArg(args, "record_id", ""), EventId: stringArg(args, "event_id", ""), Query: q})
	case "pax_logs_updates":
		filter, _ := args["filter"].(map[string]any)
		q, e := logQuery(filter)
		if e != nil {
			return nil, "", "", e
		}
		after, e := countArg(args, "after_sequence")
		if e != nil {
			return nil, "", "", e
		}
		generation, e := countArg(args, "deletion_generation")
		if e != nil {
			return nil, "", "", e
		}
		if p.cursor != "" {
			after, e = strconv.ParseUint(p.cursor, 10, 64)
			if e != nil {
				return nil, "", "", errors.New("invalid_argument")
			}
		}
		out, e := b.rpc.QueryLogUpdates(ctx, &pb.LogUpdateRequest{Filter: q, AfterSequence: after, DeletionGeneration: generation, JournalGeneration: stringArg(args, "journal_generation", ""), CountOnly: boolArg(args, "count_only"), Limit: uint32(p.limit)})
		message, err = out, e
		if e == nil && out.HasMore && !out.ResetRequired {
			cursor = strconv.FormatUint(out.NextSequence, 10)
		}
	case "pax_traffic_query":
		untilText := stringArg(args, "until", "")
		if untilText == "" {
			untilText = time.Now().UTC().Format(time.RFC3339Nano)
			args["until"] = untilText
		}
		until, e := time.Parse(time.RFC3339Nano, untilText)
		if e != nil {
			return nil, "", "", errors.New("invalid_range")
		}
		out, e := b.rpc.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: stringArg(args, "dimension", "application"), Direction: stringArg(args, "direction", "all"), Window: stringArg(args, "window", "today"), Search: stringArg(args, "search", ""), Until: timestamppb.New(until), Limit: uint32(p.limit), Cursor: p.cursor, ReadToken: p.readToken})
		message, err = out, e
		if e == nil {
			cursor, readToken = out.NextCursor, out.ReadToken
		}
	case "pax_traffic_detail":
		token := p.readToken
		if token == "" {
			token = stringArg(args, "read_token", "")
		}
		out, e := b.rpc.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{RowId: stringArg(args, "row_id", ""), ReadToken: token, ConnectionKey: stringArg(args, "connection_key", ""), DnsGroupId: stringArg(args, "dns_group_id", ""), Limit: uint32(p.limit), Cursor: p.cursor})
		message, err = out, e
		if e == nil {
			cursor, readToken = out.NextCursor, out.ReadToken
		}
	case "pax_route_history":
		from, until, e := queryRange(args, 24*time.Hour, 7*24*time.Hour)
		if e != nil {
			return nil, "", "", e
		}
		// Legacy route RPC has an inclusive upper bound. Use the preceding instant.
		out, e := b.rpc.QueryRouteHistory(ctx, &pb.RouteHistoryRequest{RouteId: stringArg(args, "route_id", ""), Metric: stringArg(args, "metric", ""), From: timestamppb.New(from), Until: timestamppb.New(until.Add(-time.Nanosecond)), Limit: uint32(p.limit), Cursor: p.cursor})
		message, err = out, e
		if e == nil {
			cursor = out.NextCursor
		}
	case "pax_route_usage":
		out, e := b.rpc.QueryRouteUsage(ctx, &pb.RouteHistoryRequest{RouteId: stringArg(args, "route_id", ""), Window: stringArg(args, "window", "today"), Limit: uint32(p.limit), Cursor: p.cursor})
		message, err = out, e
		if e == nil {
			cursor = out.NextCursor
		}
	case "pax_routes_preview":
		identity := s.operationContext(args)
		if identity.ExpectedDaemonInstance != b.hello.DaemonInstance {
			return nil, "", "", errors.New("daemon_restarted")
		}
		if !slicesContain(b.hello.Capabilities, "routes.edit_view.v1") {
			return nil, "", "", errors.New("capability_unavailable")
		}
		if _, err := b.ready(ctx); err != nil {
			return nil, "", "", err
		}
		source := stringArg(args, "source", "")
		if len(source) > 2<<20 {
			return nil, "", "", errors.New("import_too_large")
		}
		for _, line := range strings.Split(source, "\n") {
			if len(line) > 32<<10 && !boolArg(args, "is_subscription") {
				return nil, "", "", errors.New("import_too_large")
			}
		}
		out, e := b.rpc.PreviewRoutes(ctx, &pb.PreviewRoutesRequest{Source: source, IsSubscription: boolArg(args, "is_subscription"), SourceName: stringArg(args, "source_name", ""), Context: identity})
		if e != nil {
			return nil, "", "", e
		}
		if len(out.Items) > 1000 {
			return nil, "", "", errors.New("import_too_large")
		}
		message = out
	case "pax_operation_get":
		q := &pb.GetOperationRequest{DaemonInstance: stringArg(args, "daemon_instance", "")}
		if id := stringArg(args, "operation_id", ""); id != "" {
			q.Identity = &pb.GetOperationRequest_OperationId{OperationId: id}
		} else {
			q.Identity = &pb.GetOperationRequest_Request{Request: &pb.OperationRequestIdentity{ClientId: s.options.ClientID, RequestId: stringArg(args, "request_id", "")}}
		}
		op, e := b.rpc.GetOperation(ctx, q)
		if e != nil {
			return nil, "", "", e
		}
		return operationData(op), "", "", nil
	default:
		return nil, "", "", errors.New("invalid_argument")
	}
	if err != nil {
		return nil, "", "", err
	}
	data, err := projection(message)
	if err != nil {
		return nil, "", "", err
	}
	if p.tool == "pax_log_detail" {
		data["raw_state"] = "not_exposed"
	}
	// RPC cursors remain private; daemon read tokens are intentionally available
	// for detail calls, distinct from MCP continuation tokens.
	delete(data, "nextCursor")
	return data, cursor, readToken, nil
}
