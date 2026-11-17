package mcpserver

import (
	"encoding/json"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	pb "pax-connect/daemon/gen/proto/paxv1"
)

type schema = map[string]any

var queryTools = []string{"pax_status", "pax_routes_list", "pax_logs_query", "pax_log_objects", "pax_log_detail", "pax_logs_updates", "pax_traffic_query", "pax_traffic_detail", "pax_route_history", "pax_route_usage", "pax_operation_get"}
var descriptions = map[string]string{
	"pax_status":         "Read Pax status, selected/running route IDs, owner revisions and availability; never enables proxy.",
	"pax_routes_list":    "Read redacted routes, subscriptions and groups. Includes hidden routes only when requested.",
	"pax_logs_query":     "Read frozen filtered logs (default last hour, maximum 24h). No raw diagnostic text or credentials. Continuations require identical input.",
	"pax_log_objects":    "Read log object counts under the same filters; totals are not page subtotals.",
	"pax_log_detail":     "Read redacted log detail using its daemon read_token and original filter. Raw logs are not exposed.",
	"pax_logs_updates":   "Read incremental logs after a sequence. resetRequired means requery; it does not mean no updates.",
	"pax_traffic_query":  "Read traffic aggregates; default today. Retained coverage and unknown sources are preserved.",
	"pax_traffic_detail": "Read frozen traffic details; retained connections differ from long-window totals. Default 20 rows.",
	"pax_route_history":  "Read saved route measurements (default last 24h, retained at most seven days), not a new probe.",
	"pax_route_usage":    "Read actual-outbound route traffic with coverage; default today. No new probe.",
	"pax_operation_get":  "Recover a tracked operation by ID or this client's request_id. Unknown never means failed. Does not resubmit or cancel.",
}

func object(props schema, required ...string) schema {
	out := schema{"type": "object", "additionalProperties": false, "properties": props}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}
func str(max int) schema           { return schema{"type": "string", "maxLength": max} }
func idSchema() schema             { v := str(128); v["minLength"] = 1; return v }
func enum(values ...string) schema { return schema{"type": "string", "enum": values} }
func array(items schema, max int) schema {
	return schema{"type": "array", "items": items, "maxItems": max}
}
func integer(min, max int) schema { return schema{"type": "integer", "minimum": min, "maximum": max} }
func countSchema() schema         { return schema{"type": "string", "pattern": "^(0|[1-9][0-9]{0,19})$"} }
func boolean() schema             { return schema{"type": "boolean"} }
func timeSchema() schema          { return schema{"type": "string", "format": "date-time", "maxLength": 64} }
func rangeSchema() schema {
	return object(schema{"from": timeSchema(), "until": timeSchema()}, "from", "until")
}
func pageProps() schema { return schema{"limit": integer(1, 100), "continuation": str(8192)} }
func merge(dst, src schema) schema {
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func logSchema() schema {
	criteria := object(schema{"kinds": array(enum("access", "dns", "operation", "diagnostic"), 8), "levels": array(enum("info", "warning", "error"), 8), "application": str(200), "domain": str(200), "domainMode": enum("contains", "exact", "suffix"), "targetIp": str(200), "transports": array(enum("tcp", "udp"), 8), "port": integer(1, 65535), "directions": array(enum("direct", "proxy", "dns", "blocked", "unknown"), 8), "route": str(200), "rulePackage": str(200), "minLatencyMs": countSchema(), "dnsAnswerIp": str(200)})
	return object(schema{"view": enum("activity", "access", "problems", "operations"), "dimension": enum("application", "domain", "ip", "system"), "object_id": idSchema(), "search": str(200), "level": enum("info", "warning", "error"), "route_id": idSchema(), "range": rangeSchema(), "timezone": str(128), "include_dns": boolean(), "scope": enum("access", "system"), "quick": enum("all", "problems", "slow", "blocked"), "problem_id": idSchema(), "connection_key": idSchema(), "criteria": criteria})
}

func inputSchema(name string) schema {
	if isSettingsTool(name) {
		return settingsInputSchema(name)
	}
	if isCFTool(name) {
		return cfInputSchema(name)
	}
	if isProfileTool(name) {
		return profileInputSchema(name)
	}
	if isAITool(name) {
		return aiInputSchema(name)
	}
	if isNetworkTool(name) {
		return networkInputSchema(name)
	}
	if isRouteTool(name) {
		return routeInputSchema(name)
	}
	props := schema{}
	required := []string{}
	switch name {
	case "pax_routes_list":
		props = schema{"subscription_id": idSchema(), "group_id": idSchema(), "search": str(200), "include_hidden": boolean()}
		merge(props, pageProps())
	case "pax_logs_query", "pax_log_objects":
		merge(props, logSchema()["properties"].(schema))
		merge(props, pageProps())
	case "pax_log_detail":
		props = schema{"record_id": idSchema(), "event_id": idSchema(), "filter": logSchema(), "read_token": str(8192)}
		required = []string{"filter", "read_token"}
	case "pax_logs_updates":
		props = schema{"filter": logSchema(), "after_sequence": countSchema(), "deletion_generation": countSchema(), "journal_generation": idSchema(), "count_only": boolean()}
		required = []string{"filter", "after_sequence", "deletion_generation", "journal_generation"}
		merge(props, pageProps())
	case "pax_traffic_query":
		props = schema{"dimension": enum("application", "domain", "ip", "system"), "direction": enum("all", "proxy", "direct"), "window": enum("minute", "hour", "day", "today", "yesterday", "week", "month"), "search": str(200), "until": timeSchema()}
		merge(props, pageProps())
	case "pax_traffic_detail":
		props = schema{"row_id": idSchema(), "read_token": str(8192), "connection_key": idSchema(), "dns_group_id": idSchema()}
		required = []string{"row_id", "read_token"}
		merge(props, pageProps())
	case "pax_route_history":
		props = schema{"route_id": idSchema(), "metric": enum("latency", "latency_auto", "connect", "exit", "download"), "range": rangeSchema(), "timezone": str(128)}
		required = []string{"route_id", "metric"}
		merge(props, pageProps())
	case "pax_route_usage":
		props = schema{"route_id": idSchema(), "window": enum("hour", "day", "today", "week", "month")}
		required = []string{"route_id"}
		merge(props, pageProps())
	case "pax_operation_get":
		props = schema{"daemon_instance": idSchema(), "operation_id": idSchema(), "request_id": idSchema()}
		required = []string{"daemon_instance"}
	}
	out := object(props, required...)
	out["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	if name == "pax_operation_get" {
		out["oneOf"] = []any{schema{"required": []string{"operation_id"}, "not": schema{"required": []string{"request_id"}}}, schema{"required": []string{"request_id"}, "not": schema{"required": []string{"operation_id"}}}}
	}
	if name == "pax_log_detail" {
		out["oneOf"] = []any{schema{"required": []string{"record_id"}, "not": schema{"required": []string{"event_id"}}}, schema{"required": []string{"event_id"}, "not": schema{"required": []string{"record_id"}}}}
	}
	return out
}

func messageSchema(message proto.Message) schema {
	return descriptorSchema(message.ProtoReflect().Descriptor())
}
func descriptorSchema(desc protoreflect.MessageDescriptor) schema {
	if desc.FullName() == "google.protobuf.Timestamp" {
		return timeSchema()
	}
	props := schema{}
	for i := 0; i < desc.Fields().Len(); i++ {
		field := desc.Fields().Get(i)
		if field.JSONName() == "raw" {
			continue
		}
		value := fieldSchema(field)
		if field.IsMap() {
			value = schema{"type": "object", "additionalProperties": fieldSchema(field.MapValue())}
		} else if field.IsList() {
			value = schema{"type": "array", "items": value}
		}
		props[field.JSONName()] = value
	}
	return object(props)
}
func fieldSchema(field protoreflect.FieldDescriptor) schema {
	switch field.Kind() {
	case protoreflect.MessageKind:
		return descriptorSchema(field.Message())
	case protoreflect.BoolKind:
		return boolean()
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind, protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return schema{"type": "string", "pattern": "^-?[0-9]+$"}
	case protoreflect.Int32Kind, protoreflect.Uint32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind, protoreflect.Fixed32Kind:
		return schema{"type": "integer"}
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return schema{"type": "number"}
	case protoreflect.EnumKind:
		values := []string{}
		for i := 0; i < field.Enum().Values().Len(); i++ {
			values = append(values, string(field.Enum().Values().Get(i).Name()))
		}
		return enum(values...)
	default:
		return schema{"type": "string"}
	}
}

func dataSchema(name string) schema {
	if isSettingsTool(name) {
		return settingsDataSchema(name)
	}
	if isCFTool(name) {
		return cfDataSchema(name)
	}
	if isProfileTool(name) {
		return profileDataSchema(name)
	}
	if isAITool(name) {
		return aiDataSchema(name)
	}
	if isNetworkTool(name) {
		return networkDataSchema(name)
	}
	if isRouteTool(name) {
		return routeDataSchema(name)
	}
	switch name {
	case "pax_status":
		return object(schema{"daemon_version": str(128), "protocol_version": schema{"type": "integer"}, "capabilities": array(str(128), 256), "owner_app_pid": schema{"type": "integer"}, "protection_status": fieldSchema((&pb.Snapshot{}).ProtoReflect().Descriptor().Fields().ByName("status")), "selected_route_id": str(128), "running_route_id": str(128), "pending_operation_id": str(128), "degraded_reasons": array(messageSchema(&pb.ProductError{}), 100), "today_traffic": messageSchema(&pb.TrafficSnapshot{}), "current_path_check": messageSchema(&pb.CurrentPathCheck{}), "history": messageSchema(&pb.HistoryState{}), "ai_usage": messageSchema(&pb.AIUsageState{})})
	case "pax_routes_list":
		return object(schema{"catalog_revision": countSchema(), "routes": array(messageSchema(&pb.NodeProfile{}), 100), "subscriptions": array(messageSchema(&pb.SubscriptionSummary{}), 1000), "groups": array(messageSchema(&pb.RouteGroup{}), 1000)})
	case "pax_logs_query":
		return messageSchema(&pb.LogPage{})
	case "pax_log_objects":
		return messageSchema(&pb.LogObjectPage{})
	case "pax_log_detail":
		out := messageSchema(&pb.LogDetail{})
		out["properties"].(schema)["raw_state"] = enum("not_exposed")
		return out
	case "pax_logs_updates":
		return messageSchema(&pb.LogUpdatePage{})
	case "pax_traffic_query":
		return messageSchema(&pb.TrafficPage{})
	case "pax_traffic_detail":
		return messageSchema(&pb.TrafficDetail{})
	case "pax_route_history":
		return messageSchema(&pb.RouteHistoryResponse{})
	case "pax_route_usage":
		return messageSchema(&pb.RouteUsageResponse{})
	case "pax_operation_get":
		return operationSchema()
	}
	return object(schema{})
}

func outputSchema(name string) schema {
	success := object(schema{"schema_version": schema{"const": 1}, "observed_at": timeSchema(), "daemon_instance": idSchema(), "timezone": str(128), "availability": enum("complete", "partial", "unavailable"), "warnings": array(object(schema{"source": schema{"type": "string"}, "code": str(80)}, "source", "code"), 10000), "data": dataSchema(name), "paging": object(schema{"continuation": str(8192), "has_more": boolean(), "truncated": boolean(), "reason": str(128), "field_paths": array(str(200), 100)}, "has_more", "truncated")}, "schema_version", "observed_at", "daemon_instance", "timezone", "availability", "warnings", "data")
	if name == "pax_logs_query" || name == "pax_log_objects" || name == "pax_logs_updates" {
		success["properties"].(schema)["query_context"] = object(schema{"filter": logSchema()}, "filter")
	}
	if isAITool(name) {
		success["properties"].(schema)["query_context"] = aiContextSchema()
	}
	fail := object(schema{"request_id": str(128), "daemon_instance": idSchema(), "operation_id": idSchema(), "details": object(schema{"required_action": str(128), "authorization_owner": str(128), "operation_id": idSchema(), "owner_client_id": idSchema()}), "latest_revision": countSchema(), "code": str(80), "source": str(128), "retry": enum("read", "same_request", "replan", "never")}, "code", "source", "retry")
	out := schema{"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object", "oneOf": []any{success, fail}}
	if name == "pax_route_get" {
		addDocumentDefinitions(out)
	}
	return out
}

// JSON Schema counts code points; the product limit is UTF-8 bytes.
func checkInputBytes(value any, rule schema) error {
	invalid := func() error { return &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "invalid_argument"} }
	switch v := value.(type) {
	case string:
		if max, ok := rule["maxLength"].(int); ok && len(v) > max {
			return invalid()
		}
		if strings.ContainsRune(v, 0) {
			return invalid()
		}
	case map[string]any:
		props, _ := rule["properties"].(schema)
		for k, item := range v {
			child, _ := props[k].(schema)
			if err := checkInputBytes(item, child); err != nil {
				return err
			}
		}
	case []any:
		child, _ := rule["items"].(schema)
		for _, item := range v {
			if err := checkInputBytes(item, child); err != nil {
				return err
			}
		}
	}
	return nil
}

func jsonCopy(value map[string]any) map[string]any {
	raw, _ := json.Marshal(value)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}
