package mcpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

const wireResponseLimit = 256 << 10

// JSON text plus structuredContent and worst-case JSON escaping share one wire
// budget. Pagination uses this conservative payload budget before consuming rows.
const responseLimit = (wireResponseLimit - 2048) / 3
const chainLimit = 8 << 20
const cacheLimit = 32 << 20
const continuationLifetime = 10 * time.Minute

type queryPage struct {
	aiRevision, aiGeneration, aiCatalog          *uint64
	tool, signature, instance, cursor, readToken string
	args                                         map[string]any
	data                                         map[string]any
	started                                      time.Time
	count, bytes, limit                          int
}

func (s *Server) call(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	key := stringArg(args, "continuation", "")
	original := jsonCopy(args)
	delete(original, "continuation")
	signature, _ := json.Marshal(original)
	s.mu.Lock()
	// Admission bounds in-flight calls too: a single server serializes its read
	// continuations. It never holds a daemon owner lock while doing RPC I/O.
	defer s.mu.Unlock()
	for id, p := range s.pages {
		if time.Since(p.started) > continuationLifetime {
			delete(s.pages, id)
		}
	}
	var page *queryPage
	if key != "" {
		page = s.pages[key]
		if page == nil {
			return nil, errors.New("continuation_expired")
		}
		if page.tool != name || page.signature != string(signature) {
			return nil, errors.New("continuation_mismatch")
		}
	} else {
		limit := intArg(args, "limit", 50)
		if name == "pax_traffic_detail" {
			limit = intArg(args, "limit", 20)
		}
		page = &queryPage{tool: name, signature: string(signature), args: jsonCopy(original), started: time.Now(), limit: limit}
	}
	b, err := s.open(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if b.conn != nil {
			_ = b.conn.Close()
		}
	}()
	if page.instance != "" && page.instance != b.hello.DaemonInstance {
		if key != "" {
			delete(s.pages, key)
		}
		return nil, errors.New("query_changed")
	}
	page.instance = b.hello.DaemonInstance
	if key != "" && page.data != nil && (page.readToken != "" || name == "pax_logs_updates" || name == "pax_ai_logs" || name == "pax_ai_sessions") {
		// A byte-limited page may already be cached. Revalidate its owner token
		// before disclosing any remainder; cache TTL cannot extend history TTL
		// or bypass a log deletion generation change.
		probe := *page
		probe.args = jsonCopy(page.args)
		probe.limit = 1
		if name != "pax_logs_updates" {
			probe.cursor = ""
		}
		checked, _, _, err := s.query(ctx, b, &probe)
		if err != nil {
			delete(s.pages, key)
			return nil, err
		}
		if name == "pax_logs_updates" && checked["resetRequired"] == true {
			delete(s.pages, key)
			return nil, errors.New("history_expired")
		}
	}
	if page.data == nil {
		data, cursor, readToken, err := s.query(ctx, b, page)
		if err != nil {
			delete(s.pages, key)
			return nil, err
		}
		page.data = data
		page.cursor = cursor
		page.readToken = readToken
	}
	data := jsonCopy(page.data)
	out := envelope(page.instance, data)
	if isAITool(name) {
		aiMetadata(out, page, b.hello.Timezone)
	}
	if b.hello.Timezone != "" {
		out["timezone"] = b.hello.Timezone
	}
	if pfilter, ok := page.args["filter"].(map[string]any); ok && name == "pax_logs_updates" {
		out["query_context"] = map[string]any{"filter": jsonCopy(pfilter)}
	} else if name == "pax_logs_query" || name == "pax_log_objects" {
		filter := jsonCopy(page.args)
		delete(filter, "limit")
		out["query_context"] = map[string]any{"filter": filter}
	}
	if !hasPageInput(name) {
		raw, _ := json.Marshal(out)
		if len(raw) > responseLimit {
			return nil, errors.New("result_too_large")
		}
		return out, nil
	}
	paths := []string{}
	rows := 0
	// Split complete array elements. The daemon cursor is retained with all
	// unsent elements, so byte limiting never skips already-fetched rows.
	keys := pageArrayPaths(name, data)
	for _, field := range keys {
		setPageArray(data, field, []any{})
	}
	raw, _ := json.Marshal(out)
	if len(raw) > responseLimit-16384 {
		return nil, errors.New("result_too_large")
	}
	remaining := jsonCopy(page.data)
	full := false
	for _, field := range keys {
		values := pageArray(page.data, field)
		sent := 0
		for sent < len(values) && rows < page.limit && !full {
			current := pageArray(data, field)
			setPageArray(data, field, append(current, values[sent]))
			encoded, _ := json.Marshal(out)
			if len(encoded) > responseLimit-16384 {
				setPageArray(data, field, current)
				full = true
				break
			}
			sent++
			rows++
		}
		setPageArray(remaining, field, values[sent:])
		if sent < len(values) {
			paths = append(paths, "data."+field)
		}
	}
	if len(paths) > 0 && rows == 0 {
		return nil, errors.New("result_too_large")
	}
	hasPending := len(paths) > 0
	if hasPending && name == "pax_logs_updates" {
		data["nextSequence"] = stringArg(page.args, "after_sequence", "0")
		data["hasMore"] = true
		data["exact"] = false
	}
	nextCount := page.count + rows
	encoded, _ := json.Marshal(out)
	nextBytes := page.bytes + len(encoded)
	if nextCount > 10000 || nextBytes > chainLimit {
		if key != "" {
			delete(s.pages, key)
		}
		return nil, errors.New("query_limit")
	}
	more := hasPending || page.cursor != ""
	if more {
		if key == "" {
			var token [24]byte
			if _, err = rand.Read(token[:]); err != nil {
				return nil, err
			}
			key = hex.EncodeToString(token[:])
		}
		next := *page
		next.data = remaining
		next.count = nextCount
		next.bytes = nextBytes
		if !hasPending {
			next.data = nil
		}
		cacheBytes := 0
		for id, other := range s.pages {
			if id != key {
				raw, _ := json.Marshal(other.data)
				cacheBytes += len(raw)
			}
		}
		pending, _ := json.Marshal(next.data)
		if len(pending) > chainLimit || cacheBytes+len(pending) > cacheLimit || (s.pages[key] == nil && len(s.pages) >= 16) {
			return nil, errors.New("query_busy")
		}
		s.pages[key] = &next
		out["paging"] = map[string]any{"continuation": key, "has_more": true, "truncated": hasPending, "field_paths": paths}
	} else {
		if key != "" {
			delete(s.pages, key)
		}
		if hasPageInput(name) {
			out["paging"] = map[string]any{"has_more": false, "truncated": false}
		}
	}
	if timezone := stringArg(page.args, "timezone", ""); timezone != "" {
		out["timezone"] = timezone
	}
	return out, nil
}

func hasPageInput(name string) bool {
	_, ok := inputSchema(name)["properties"].(schema)["continuation"]
	return ok
}

// AI projections page whole business records, not their component arrays.
func pageArrayPaths(name string, data map[string]any) []string {
	var paths []string
	switch name {
	case "pax_cf_workspace":
		paths = []string{"workspace.records", "workspace.networks", "candidates.items"}
	case "pax_ai_buckets":
		paths = []string{"buckets"}
	case "pax_ai_sessions":
		paths = []string{"items"}
	case "pax_ai_logs":
		paths = []string{"requests"}
	case "pax_ai_analysis":
		paths = []string{"contextUsage", "rows", "requests", "limits.primary", "limits.secondary", "limits.trend.requests", "limits.trend.switches"}
	default:
		for field, v := range data {
			if _, ok := v.([]any); ok {
				paths = append(paths, field)
			}
		}
	}
	out := []string{}
	for _, path := range paths {
		if pageArray(data, path) != nil {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}
func pageArray(data map[string]any, path string) []any {
	parts := strings.Split(path, ".")
	for _, part := range parts[:len(parts)-1] {
		data, _ = data[part].(map[string]any)
	}
	value, _ := data[parts[len(parts)-1]].([]any)
	return value
}
func setPageArray(data map[string]any, path string, value []any) {
	parts := strings.Split(path, ".")
	for _, part := range parts[:len(parts)-1] {
		data = data[part].(map[string]any)
	}
	data[parts[len(parts)-1]] = value
}
