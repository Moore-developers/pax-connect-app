package mcpserver

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	pb "pax-connect/daemon/gen/proto/paxv1"
)

var aiErrorCodes = map[string]bool{"ai_usage_unavailable": true, "ai_usage_busy": true, "ai_usage_cancelled": true, "ai_usage_range_invalid": true, "query_changed": true, "page_token_expired": true, "ai_catalog_unavailable": true, "session_id_too_long": true}

func aiRange(args map[string]any, daemonZone string) (time.Time, time.Time, error) {
	zone := stringArg(args, "timezone", daemonZone)
	location, err := time.LoadLocation(zone)
	if err != nil || zone == "" || zone == "Local" {
		return time.Time{}, time.Time{}, errors.New("invalid_range")
	}
	args["timezone"] = zone
	if _, ok := args["range"]; !ok {
		now := time.Now().In(location)
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
		args["range"] = map[string]any{"from": start.Format(time.RFC3339Nano), "until": now.Format(time.RFC3339Nano)}
	}
	from, until, err := queryRange(args, 0, 93*24*time.Hour)
	if err != nil || from.UnixMilli() <= 0 || from.UnixMilli() >= until.UnixMilli() {
		return from, until, errors.New("invalid_range")
	}
	// Return the actual millisecond boundaries used by AIUsageService.
	from, until = time.UnixMilli(from.UnixMilli()), time.UnixMilli(until.UnixMilli())
	args["range"] = map[string]any{"from": from.Format(time.RFC3339Nano), "until": until.Format(time.RFC3339Nano)}
	return from, until, nil
}

func aiSession(value any) *pb.AIUsageSessionKey {
	args, _ := value.(map[string]any)
	return &pb.AIUsageSessionKey{Cli: stringArg(args, "cli", ""), SessionId: stringArg(args, "session_id", "")}
}

func (s *Server) queryAI(ctx context.Context, b *backend, p *queryPage) (map[string]any, string, string, error) {
	args := p.args
	if !slicesContain(b.hello.Capabilities, "ai.usage.v1") {
		return nil, "", "", errors.New("capability_unavailable")
	}
	var from, until time.Time
	var err error
	if p.tool != "pax_ai_summary" {
		from, until, err = aiRange(args, b.hello.Timezone)
		if err != nil {
			return nil, "", "", err
		}
	}
	var message proto.Message
	cursor := ""
	switch p.tool {
	case "pax_ai_summary":
		message, err = b.ai.SummarizeAIUsage(ctx, &pb.AIUsageSummaryQuery{})
	case "pax_ai_buckets":
		if !slicesContain(b.hello.Capabilities, "ai.usage.buckets.timezone.v1") {
			return nil, "", "", errors.New("capability_unavailable")
		}
		q := &pb.AIUsageQuery{FromMs: from.UnixMilli(), ToMs: until.UnixMilli(), Granularity: stringArg(args, "granularity", "day"), Timezone: stringArg(args, "timezone", "")}
		if _, ok := args["known_revision"]; ok {
			v, e := countArg(args, "known_revision")
			if e != nil {
				return nil, "", "", e
			}
			q.KnownRevision = &v
		}
		message, err = b.ai.QueryAIUsageBuckets(ctx, q)
	case "pax_ai_logs":
		if !slicesContain(b.hello.Capabilities, "ai.usage.logs.snapshot.v1") {
			return nil, "", "", errors.New("capability_unavailable")
		}
		q := &pb.AIUsageLogQuery{FromMs: from.UnixMilli(), ToMs: until.UnixMilli(), Cli: stringArg(args, "cli", ""), ProfileId: stringArg(args, "profile_id", ""), ModelId: stringArg(args, "model_id", ""), ServiceSource: stringArg(args, "service_source", ""), Query: stringArg(args, "search", ""), ExpectedRevision: p.aiRevision, ExpectedGeneration: p.aiGeneration}
		if v, ok := args["succeeded"].(bool); ok {
			q.Succeeded = &v
		}
		for _, bound := range []struct {
			name string
			dst  **int64
		}{{"min_official_cost_micro", &q.MinOfficialCostMicro}, {"max_official_cost_micro", &q.MaxOfficialCostMicro}} {
			if _, ok := args[bound.name]; ok {
				v, e := strconv.ParseInt(stringArg(args, bound.name, ""), 10, 64)
				if e != nil {
					return nil, "", "", errors.New("invalid_argument")
				}
				*bound.dst = &v
			}
		}
		if p.cursor != "" {
			v, e := strconv.ParseUint(p.cursor, 10, 32)
			if e != nil {
				return nil, "", "", errors.New("invalid_argument")
			}
			q.Page = uint32(v)
		}
		out, e := b.ai.ListAIUsageLogs(ctx, q)
		if e != nil {
			return nil, "", "", e
		}
		if out.State == nil {
			return nil, "", "", errors.New("ai_usage_unavailable")
		}
		p.aiRevision, p.aiGeneration = &out.State.Revision, &out.State.Generation
		if int64(q.Page+1)*10 < out.GetTotals().GetCount() {
			cursor = strconv.FormatUint(uint64(q.Page+1), 10)
		}
		message = out
	case "pax_ai_sessions":
		if !slicesContain(b.hello.Capabilities, "ai.sessions.catalog.activity.v1") {
			return nil, "", "", errors.New("capability_unavailable")
		}
		q := &pb.AISessionCatalogQuery{FromMs: from.UnixMilli(), ToMs: until.UnixMilli(), Cli: stringArg(args, "cli", ""), Query: stringArg(args, "search", ""), PageSize: uint32(p.limit), PageToken: p.cursor}
		if selected, ok := args["selected"]; ok {
			q.Selected = aiSession(selected)
		}
		out, e := b.ai.ListAISessions(ctx, q)
		if e != nil {
			return nil, "", "", e
		}
		if out.ResetRequired || p.aiRevision != nil && (*p.aiRevision != out.UsageRevision || *p.aiCatalog != out.Revision) {
			return nil, "", "", errors.New("page_token_expired")
		}
		p.aiRevision, p.aiCatalog = &out.UsageRevision, &out.Revision
		cursor, message = out.NextPageToken, out
	case "pax_ai_analysis":
		projection := stringArg(args, "projection", "context")
		q := &pb.AIUsageAnalysisQuery{FromMs: from.UnixMilli(), ToMs: until.UnixMilli(), LimitsOnly: projection == "limits"}
		keys, _ := args["sessions"].([]any)
		for _, key := range keys {
			q.Sessions = append(q.Sessions, aiSession(key))
		}
		selected := q.Sessions
		if projection == "context" {
			q.Sessions = nil
		}
		if q.LimitsOnly && len(q.Sessions) > 1 {
			return nil, "", "", errors.New("invalid_argument")
		}
		capability := "ai.usage.context.v1"
		if projection == "trajectories" {
			capability = "ai.usage.sessions.v1"
		}
		if q.LimitsOnly {
			capability = "ai.usage.limits.v1"
		}
		if !slicesContain(b.hello.Capabilities, capability) {
			return nil, "", "", errors.New("capability_unavailable")
		}
		if comparison, ok := args["comparison_range"]; ok {
			if !q.LimitsOnly {
				return nil, "", "", errors.New("invalid_argument")
			}
			start, end, e := queryRange(map[string]any{"range": comparison}, 0, 367*24*time.Hour)
			if e != nil || end.After(from) {
				return nil, "", "", errors.New("invalid_range")
			}
			a, z := start.UnixMilli(), end.UnixMilli()
			q.ComparisonFromMs, q.ComparisonToMs = &a, &z
		}
		if _, ok := args["known_revision"]; ok {
			v, e := countArg(args, "known_revision")
			if e != nil {
				return nil, "", "", e
			}
			q.KnownRevision = &v
		}
		out, e := b.ai.QueryAIUsageAnalysis(ctx, q)
		if e != nil {
			return nil, "", "", e
		}
		if projection != "context" {
			out.ContextUsage = nil
		}
		if projection == "context" && len(selected) > 0 {
			filtered := out.ContextUsage[:0]
			for _, row := range out.ContextUsage {
				for _, key := range selected {
					if row.Cli == key.Cli && row.SessionId == key.SessionId {
						filtered = append(filtered, row)
						break
					}
				}
			}
			out.ContextUsage = filtered
		}
		if projection != "trajectories" {
			out.Rows, out.Requests = nil, nil
		}
		if !q.LimitsOnly {
			out.Limits = nil
		}
		message = out
	}
	if err != nil {
		return nil, "", "", err
	}
	data, err := projection(message)
	if err != nil {
		return nil, "", "", err
	}
	delete(data, "nextPageToken")
	if err = redactAITitles(data, p.tool == "pax_ai_sessions"); err != nil {
		return nil, "", "", err
	}
	return data, cursor, "", nil
}

// Analysis titles have no source provenance on the wire, so expose only identity.
func redactAITitles(value any, catalog bool) error {
	switch v := value.(type) {
	case map[string]any:
		if id, ok := v["sessionId"].(string); ok && len(id) > 128 {
			return errors.New("session_id_too_long")
		}
		if catalog {
			source, _ := v["titleSource"].(string)
			if strings.Contains(source, "prompt") || strings.Contains(source, "message") {
				key, _ := v["key"].(map[string]any)
				v["title"] = shortSessionID(stringArg(key, "sessionId", ""))
				v["title_redacted"] = true
			}
		} else if id := stringArg(v, "sessionId", ""); id != "" {
			for _, field := range []string{"sessionTitle", "title"} {
				if _, ok := v[field]; ok {
					v[field] = shortSessionID(id)
				}
			}
		}
		for _, child := range v {
			if err := redactAITitles(child, catalog); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range v {
			if err := redactAITitles(child, catalog); err != nil {
				return err
			}
		}
	}
	return nil
}

func shortSessionID(id string) string {
	if len(id) > 12 {
		return string([]rune(id)[:min(12, len([]rune(id)))])
	}
	return id
}

func validateAIArgs(args map[string]any) error {
	if _, ok := args["known_revision"]; ok {
		if _, err := countArg(args, "known_revision"); err != nil {
			return invalidArgument()
		}
	}
	var minimum, maximum *int64
	for _, bound := range []struct {
		name string
		dst  **int64
	}{{"min_official_cost_micro", &minimum}, {"max_official_cost_micro", &maximum}} {
		if _, ok := args[bound.name]; ok {
			v, err := strconv.ParseInt(stringArg(args, bound.name, ""), 10, 64)
			if err != nil {
				return invalidArgument()
			}
			*bound.dst = &v
		}
	}
	if minimum != nil && maximum != nil && *minimum > *maximum {
		return invalidArgument()
	}
	if v, ok := args["model_id"].(string); ok && strings.TrimSpace(v) == "" {
		return invalidArgument()
	}
	return nil
}
