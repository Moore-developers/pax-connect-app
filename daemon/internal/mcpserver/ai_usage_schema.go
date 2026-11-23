package mcpserver

import pb "pax-connect/daemon/gen/proto/paxv1"

var aiTools = []string{"pax_ai_summary", "pax_ai_buckets", "pax_ai_sessions", "pax_ai_analysis", "pax_ai_logs"}

func isAITool(name string) bool { return slicesContain(aiTools, name) }

func aiDescription(name string) string {
	switch name {
	case "pax_ai_summary":
		return "Read lifetime hourly-ledger totals. No arbitrary date filter; detail retention does not limit these totals. Money is stored micro-USD, official 1x and historical channel charges. Does not reprice."
	case "pax_ai_buckets":
		return "Read complete hour/day aggregates before paging, plus lastUsed and owner state. Default today in daemon timezone; maximum 93 days. notModified is not empty usage. Continuations require identical inputs."
	case "pax_ai_sessions":
		return "Read native main-session catalog, including empty sessions, source errors and selected identity. Default today; maximum 93 days. Message-derived titles are redacted. Changed catalog invalidates continuation."
	case "pax_ai_analysis":
		return "Read daemon context, retained trajectories or limits projection, preserving child-agent completeness and unknown costs. Context uses date range, selected trajectories include all retained dates; limits accepts at most one session. Maximum 93-day input range. No request or response text."
	default:
		return "Read request metadata with whole-filter totals, official 1x and historical channel micro-USD. Default today, maximum 93 days. Result-tab counts exclude the succeeded filter. Version-bound continuations return query_changed after append/delete/reprice; start a new query. Never exposes prompt/response or reprices."
	}
}

func aiSessionSchema() schema {
	return object(schema{"cli": enum("claude", "codex", "dsh", "antigravity"), "session_id": idSchema()}, "cli", "session_id")
}

func aiInputSchema(name string) schema {
	props := schema{}
	if name != "pax_ai_summary" {
		merge(props, schema{"range": rangeSchema(), "timezone": str(128)})
		merge(props, pageProps())
	}
	switch name {
	case "pax_ai_buckets":
		merge(props, schema{"granularity": enum("hour", "day"), "known_revision": revisionSchema()})
	case "pax_ai_sessions":
		merge(props, schema{"cli": enum("claude", "codex", "dsh", "antigravity"), "search": str(200), "selected": aiSessionSchema()})
	case "pax_ai_analysis":
		merge(props, schema{"sessions": array(aiSessionSchema(), 5), "projection": enum("context", "trajectories", "limits"), "comparison_range": rangeSchema(), "known_revision": revisionSchema()})
	case "pax_ai_logs":
		merge(props, schema{"cli": enum("claude", "codex", "dsh", "antigravity"), "profile_id": idSchema(), "model_id": nameSchema(), "service_source": enum("official_account", "official_api", "relay_api", "unknown"), "succeeded": boolean(), "search": str(200), "min_official_cost_micro": countSchema(), "max_official_cost_micro": countSchema()})
	}
	out := object(props)
	out["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	if name == "pax_ai_analysis" {
		out["oneOf"] = []any{
			schema{"properties": schema{"projection": enum("context", "trajectories")}, "not": schema{"required": []string{"comparison_range"}}},
			schema{"required": []string{"projection"}, "properties": schema{"projection": enum("limits"), "sessions": array(aiSessionSchema(), 1)}},
		}
	}
	return out
}

func aiDataSchema(name string) schema {
	switch name {
	case "pax_ai_summary", "pax_ai_buckets":
		return messageSchema(&pb.AIUsageBuckets{})
	case "pax_ai_logs":
		return messageSchema(&pb.AIUsageLogs{})
	case "pax_ai_sessions":
		out := messageSchema(&pb.AISessionCatalogPage{})
		props := out["properties"].(schema)
		delete(props, "nextPageToken")
		for _, entry := range []schema{props["items"].(schema)["items"].(schema), props["selected"].(schema)} {
			entry["properties"].(schema)["title_redacted"] = boolean()
		}
		return out
	default:
		return messageSchema(&pb.AIUsageAnalysis{})
	}
}
