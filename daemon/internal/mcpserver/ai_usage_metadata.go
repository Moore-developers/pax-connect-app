package mcpserver

import "time"

func aiContextSchema() schema {
	return object(schema{"range": rangeSchema(), "bucket_range": rangeSchema(), "aggregation_timezone": str(128), "granularity": enum("hour", "day"), "coverage": enum("permanent_hourly_ledger", "retained_requests", "native_catalog", "retained_session_trajectories"), "money_units": object(schema{"integer_costs": enum("micro_usd"), "limit_money": enum("usd")}, "integer_costs"), "official_price_basis": enum("stored_1x"), "channel_price_basis": enum("stored_historical"), "title_policy": enum("message_titles_redacted", "identity_only"), "retention_days": integer(93, 93)}, "coverage", "money_units", "official_price_basis", "channel_price_basis")
}

func aiMetadata(out map[string]any, page *queryPage, daemonZone string) {
	data := out["data"].(map[string]any)
	context := map[string]any{"coverage": "retained_requests", "money_units": map[string]any{"integer_costs": "micro_usd"}, "official_price_basis": "stored_1x", "channel_price_basis": "stored_historical"}
	if r, ok := page.args["range"]; ok {
		context["range"] = r
	}
	switch page.tool {
	case "pax_ai_summary":
		context["coverage"] = "permanent_hourly_ledger"
	case "pax_ai_buckets":
		context["coverage"] = "permanent_hourly_ledger"
		context["aggregation_timezone"] = stringArg(page.args, "timezone", daemonZone)
		context["granularity"] = stringArg(page.args, "granularity", "day")
		// Permanent aggregates resolve whole UTC hours; do not claim exact request-time cuts.
		r := page.args["range"].(map[string]any)
		start, _ := time.Parse(time.RFC3339Nano, stringArg(r, "from", ""))
		end, _ := time.Parse(time.RFC3339Nano, stringArg(r, "until", ""))
		lower, upper := bucketBoundary(start), bucketBoundary(end)
		context["bucket_range"] = map[string]any{"from": lower.Format(time.RFC3339Nano), "until": upper.Format(time.RFC3339Nano)}
		if !lower.Equal(start) || !upper.Equal(end) {
			aiWarning(out, "data.buckets", "hour_bucket_resolution")
		}
	case "pax_ai_sessions":
		context["coverage"] = "native_catalog"
		context["title_policy"] = "message_titles_redacted"
		if data["ready"] == false {
			aiWarning(out, "data", "catalog_not_ready")
			out["availability"] = "unavailable"
		}
	case "pax_ai_analysis":
		context["retention_days"] = 93
		context["title_policy"] = "identity_only"
		if stringArg(page.args, "projection", "context") == "limits" {
			context["money_units"].(map[string]any)["limit_money"] = "usd"
		}
		if stringArg(page.args, "projection", "context") == "trajectories" && lenAnySessions(page.args) > 0 {
			context["coverage"] = "retained_session_trajectories"
		}
	case "pax_ai_logs":
		context["retention_days"] = 93
	}
	if page.tool == "pax_ai_logs" || page.tool == "pax_ai_analysis" {
		if r, ok := page.args["range"].(map[string]any); ok {
			start, _ := time.Parse(time.RFC3339Nano, stringArg(r, "from", ""))
			retained := time.Now().AddDate(0, 0, -93)
			if ms, err := countArg(data, "retainedFromMs"); err == nil && ms > 0 {
				retained = time.UnixMilli(int64(ms))
			}
			if start.Before(retained) {
				aiWarning(out, "data", "detail_retention")
			}
		}
	}
	aiAvailability(out, data, "data")
	out["query_context"] = context
}

func aiWarning(out map[string]any, source, code string) {
	warnings := out["warnings"].([]any)
	// Repeated row-level unknowns need one source warning, not an unbounded list.
	for _, v := range warnings {
		w := v.(map[string]any)
		if w["source"] == source && w["code"] == code {
			return
		}
	}
	out["warnings"] = append(warnings, map[string]any{"source": source, "code": code})
	if out["availability"] == "complete" {
		out["availability"] = "partial"
	}
}

func aiAvailability(out map[string]any, value any, path string) {
	switch v := value.(type) {
	case map[string]any:
		if requests, ok := v["requests"].(string); ok && requests != "0" {
			total, _ := countArg(v, "requests")
			for _, field := range []struct{ key, warning string }{{"pricedRequests", "price_incomplete"}, {"knownTurnRequests", "turns_incomplete"}} {
				if _, present := v[field.key]; present {
					known, _ := countArg(v, field.key)
					if known < total {
						aiWarning(out, path, field.warning)
					}
				}
			}
		}
		for key, item := range v {
			switch key {
			case "unpriced":
				if count, ok := item.(string); ok && count != "0" {
					aiWarning(out, path, "price_incomplete")
				}
			case "unconfirmedChannel":
				if text, ok := item.(string); ok && text != "0" {
					aiWarning(out, path, "channel_cost_unconfirmed")
				}
			case "channelUnknown", "subagentsIncomplete", "agentsIncomplete":
				if item == true {
					aiWarning(out, path, key)
				}
			case "turnsComplete":
				if item == false {
					aiWarning(out, path, "turns_incomplete")
				}
			case "available":
				if item == false {
					aiWarning(out, path, "day_unavailable")
				}
			case "catalogError":
				if code, ok := item.(string); ok && code != "" {
					aiWarning(out, path, safeCode(code))
				}
			case "skipped":
				if count, ok := item.(string); ok && count != "0" {
					aiWarning(out, path, "collection_skipped")
				}
			case "errorCode":
				if item == "ai_usage_rebuild_required" {
					out["availability"] = "unavailable"
				}
			}
			aiAvailability(out, item, path+"."+key)
		}
	case []any:
		for _, item := range v {
			aiAvailability(out, item, path)
		}
	}
}

func lenAnySessions(args map[string]any) int {
	sessions, _ := args["sessions"].([]any)
	return len(sessions)
}

func bucketBoundary(at time.Time) time.Time {
	floor := at.UTC().Truncate(time.Hour)
	if at.After(floor) {
		return floor.Add(time.Hour)
	}
	return floor
}
