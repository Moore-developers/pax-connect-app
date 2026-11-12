package aiprofile

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// ModelInfo contains only facts returned by the provider's model directory.
// Availability in this list does not prove protocol compatibility or inference success.
type ModelInfo struct {
	ID            string `json:"id"`
	Owner         string `json:"owner,omitempty"`
	Name          string `json:"name,omitempty"`
	ContextWindow int64  `json:"context_window,omitempty"`
}
type modelResult struct {
	Models   []ModelInfo
	Endpoint string
	Partial  bool
}

// RefreshModels performs a bounded read-only provider request outside the configuration owner lock.
func (m *Manager) RefreshModels(ctx context.Context, id string) (Workspace, error) {
	m.mu.Lock()
	if m.blocked {
		m.mu.Unlock()
		return Workspace{}, RecoveryRequired
	}
	p, ok := m.profiles[id]
	client := m.modelClient
	if !ok || p.Kind == "native" || client == nil {
		m.mu.Unlock()
		return Workspace{}, Unsupported
	}
	m.mu.Unlock()
	deadline, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	result, fetchError := fetchModels(deadline, client, p.BaseURL, p.Key)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.blocked || m.recovery.Pending != nil {
		return Workspace{}, RecoveryRequired
	}
	current, exists := m.profiles[id]
	if !exists || current.ConnectionRevision != p.ConnectionRevision || current.Revision != p.Revision {
		return Workspace{}, Conflict
	}
	if ctx.Err() != nil {
		return Workspace{}, ctx.Err()
	}
	current.ModelsChecked = time.Now().Unix()
	current.ModelsState = "ready"
	current.ModelsError = ""
	if fetchError != nil {
		current.ModelsState = "failed"
		current.ModelsError = modelError(fetchError)
	} else {
		current.Catalog = result.Models
		current.ModelsEndpoint = result.Endpoint
		if result.Partial {
			current.ModelsState = "partial"
		}
		current.ModelIDs = []string{}
		for _, v := range result.Models {
			current.ModelIDs = append(current.ModelIDs, v.ID)
		}
	}
	current.Revision++
	if e := m.saveProfile(ctx, current); e != nil {
		return Workspace{}, e
	}
	return m.workspace(ctx)
}
func modelError(e error) string {
	var f Failure
	if errors.As(e, &f) {
		return string(f)
	}
	var timeout net.Error
	if errors.As(e, &timeout) && timeout.Timeout() || errors.Is(e, context.DeadlineExceeded) {
		return "models_timeout"
	}
	return "models_unavailable"
}
func modelID(s string) bool {
	return strings.TrimSpace(s) != "" && len(s) <= 256 && !strings.ContainsAny(s, "\r\n\x00")
}
func fetchModels(ctx context.Context, baseClient *http.Client, base, key string) (modelResult, error) {
	u, e := url.Parse(base)
	if e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return modelResult{}, Invalid
	}
	client := *baseClient
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	prefix := strings.TrimRight(u.Path, "/")
	routes := []string{prefix + "/models"}
	if !strings.HasSuffix(prefix, "/v1") {
		routes = append(routes, prefix+"/v1/models")
	}
	for i, route := range routes {
		endpoint := *u
		endpoint.Path = route
		endpoint.RawPath = ""
		result, e := readModelPages(ctx, &client, &endpoint, key)
		if e == nil {
			return result, nil
		}
		if i+1 == len(routes) || e != Failure("models_not_found") {
			return modelResult{}, e
		}
	}
	return modelResult{}, Failure("models_not_found")
}
func readModelPages(ctx context.Context, client *http.Client, endpoint *url.URL, key string) (modelResult, error) {
	result := modelResult{Endpoint: endpoint.String()}
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 5; page++ {
		next := *endpoint
		if cursor != "" {
			q := next.Query()
			q.Set("after_id", cursor)
			next.RawQuery = q.Encode()
		}
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, next.String(), nil)
		if e != nil {
			return result, Invalid
		}
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("x-api-key", key)
		response, e := client.Do(req)
		if e != nil {
			return result, e
		}
		b, e := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
		_ = response.Body.Close()
		if e != nil {
			return result, e
		}
		if len(b) > 2<<20 {
			return result, Failure("models_invalid_response")
		}
		switch response.StatusCode {
		case http.StatusNotFound:
			return result, Failure("models_not_found")
		case http.StatusUnauthorized, http.StatusForbidden:
			return result, Failure("models_auth_failed")
		case http.StatusOK:
		default:
			return result, Failure("models_unavailable")
		}
		var payload struct {
			Data []struct {
				ID      string `json:"id"`
				Owner   string `json:"owned_by"`
				Name    string `json:"display_name"`
				Context int64  `json:"context_window"`
			} `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if json.Unmarshal(b, &payload) != nil || payload.Data == nil {
			return result, Failure("models_invalid_response")
		}
		for index, v := range payload.Data {
			if !modelID(v.ID) || len(v.Owner) > 256 || len(v.Name) > 256 || v.Context < 0 {
				return result, Failure("models_invalid_response")
			}
			if seen[v.ID] {
				continue
			}
			seen[v.ID] = true
			result.Models = append(result.Models, ModelInfo{ID: v.ID, Owner: v.Owner, Name: v.Name, ContextWindow: v.Context})
			if len(result.Models) >= 1000 {
				result.Partial = payload.HasMore || index+1 < len(payload.Data)
				sortModels(&result)
				return result, nil
			}
		}
		if !payload.HasMore {
			sortModels(&result)
			return result, nil
		}
		if !modelID(payload.LastID) || payload.LastID == cursor {
			result.Partial = true
			sortModels(&result)
			return result, nil
		}
		cursor = payload.LastID
	}
	result.Partial = true
	sortModels(&result)
	return result, nil
}
func sortModels(r *modelResult) {
	sort.Slice(r.Models, func(i, j int) bool { return r.Models[i].ID < r.Models[j].ID })
	if r.Models == nil {
		r.Models = []ModelInfo{}
	}
}
