package aiprofile

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestModelDiscoveryPersistsCatalogAndUsesPrefixFallback(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer provider-key" {
			t.Error("missing credential")
		}
		if r.URL.Path == "/proxy/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"actual-model-a","owned_by":"provider","context_window":128000},{"id":"actual-model-b"}]}`))
	}))
	defer server.Close()
	m, _ := claudeFixture(t)
	m.modelClient = server.Client()
	p := Profile{ID: "4eb87612-9a70-49d3-88e0-a54d40245269", Name: "Catalog", BaseURL: server.URL + "/proxy", Multiplier: 1}
	w, e := m.Mutate(context.Background(), Mutation{Action: "save", OperationID: "create-models", Profile: p, Key: "provider-key"})
	if e != nil {
		t.Fatal(e)
	}
	var found Profile
	for _, v := range w.Profiles {
		if v.ID == p.ID {
			found = v
		}
	}
	if found.ModelsState != "ready" || len(found.Catalog) != 2 || len(found.ModelIDs) != 2 {
		t.Fatal("successful directory was not published")
	}
	if found.Catalog[0].ID != "actual-model-a" || found.Catalog[0].ContextWindow != 128000 {
		t.Fatal("model facts not preserved")
	}
	if len(calls) != 2 || calls[0] != "/proxy/models" || calls[1] != "/proxy/v1/models" {
		t.Fatal("incorrect endpoint resolution")
	}
	if !strings.HasSuffix(found.ModelsEndpoint, "/proxy/v1/models") {
		t.Fatal("discovery source not recorded")
	}
	if _, e = m.Mutate(context.Background(), Mutation{Action: "save", OperationID: "create-models", Profile: p, Key: "provider-key"}); e != nil {
		t.Fatal("discovery invalidated idempotent create")
	}
	if len(calls) != 2 {
		t.Fatal("completed create retried directory request")
	}
	if _, e = m.Plan(context.Background(), PlanRequest{CLI: Claude, ProfileID: p.ID}); e == nil {
		t.Fatal("models discovery initialized CLI template")
	}
}

func TestFailedModelRefreshKeepsPreviousCatalogAndDoesNotRegenerate(t *testing.T) {
	failing := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"do-not-publish-provider-response"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"saved-model"}]}`))
	}))
	defer server.Close()
	m, home := claudeFixture(t)
	w := importClaude(t, m)
	p := w.Profiles[0]
	p.BaseURL = server.URL
	w, e := m.Mutate(context.Background(), Mutation{Action: "save", OperationID: "change", ExpectedRevision: p.Revision, Profile: p, Key: "provider-key"})
	if e != nil {
		t.Fatal(e)
	}
	m.modelClient = server.Client()
	w, e = m.RefreshModels(context.Background(), p.ID)
	if e != nil {
		t.Fatal(e)
	}
	generated := filepath.Join(home, ".claude", "settings.json")
	before, e := os.Stat(generated)
	if e != nil {
		t.Fatal(e)
	}
	failing = true
	w, e = m.RefreshModels(context.Background(), p.ID)
	if e != nil {
		t.Fatal(e)
	}
	p = w.Profiles[0]
	if p.ModelsState != "failed" || p.ModelsError != "models_auth_failed" || len(p.Catalog) != 1 || p.Catalog[0].ID != "saved-model" {
		t.Fatal("failed refresh erased catalog or leaked raw error")
	}
	after, _ := os.Stat(generated)
	if !os.SameFile(before, after) {
		t.Fatal("metadata request regenerated CLI files")
	}
	_ = m.Close()
	restored, e := New(Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid())})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = restored.Close() }()
	w, e = restored.Query(context.Background())
	if e != nil || len(w.Profiles[0].Catalog) != 1 {
		t.Fatal("catalog not persisted across restart")
	}
}
func TestModelsRedirectNeverForwardsCredential(t *testing.T) {
	reached := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true; w.WriteHeader(http.StatusOK) }))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer origin.Close()
	if _, e := fetchModels(context.Background(), origin.Client(), origin.URL, "secret-key"); e == nil {
		t.Fatal("redirect was accepted")
	}
	if reached {
		t.Fatal("credential-bearing model request followed a redirect")
	}
}

func TestModelRefreshCannotOverwritePendingRecovery(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		_, _ = w.Write([]byte(`{"data":[{"id":"new-model"}]}`))
	}))
	defer server.Close()
	m, _ := claudeFixture(t)
	w, e := m.Mutate(context.Background(), Mutation{Action: "save", OperationID: "create", Profile: Profile{ID: "4eb87612-9a70-49d3-88e0-a54d40245269", Name: "Provider", BaseURL: server.URL, Multiplier: 1}, Key: "key"})
	if e != nil {
		t.Fatal(e)
	}
	m.modelClient = server.Client()
	done := make(chan error, 1)
	go func() { _, err := m.RefreshModels(context.Background(), w.Profiles[0].ID); done <- err }()
	<-started
	m.mu.Lock()
	e = m.saveJournal(context.Background(), pending{Schema: 2, CLI: Claude, Directory: m.targetDir(Claude)})
	m.blocked = true
	m.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	close(release)
	if e = <-done; e != RecoveryRequired {
		t.Fatal("late catalog write overwrote recovery state")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.recovery.Pending == nil || m.recovery.Pending.CLI != Claude {
		t.Fatal("recovery evidence lost")
	}
}
func TestModelDirectoryTruncationIsPartialAcrossPages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset := 0
		if r.URL.Query().Get("after_id") != "" {
			offset = 600
		}
		data := []map[string]string{}
		for i := 0; i < 600; i++ {
			data = append(data, map[string]string{"id": fmt.Sprintf("model-%04d", i+offset)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "has_more": offset == 0, "last_id": "model-0599"})
	}))
	defer server.Close()
	result, e := fetchModels(context.Background(), server.Client(), server.URL, "key")
	if e != nil {
		t.Fatal(e)
	}
	if len(result.Models) != 1000 || !result.Partial {
		t.Fatal("truncated multi-page catalog incorrectly reported complete")
	}
}
