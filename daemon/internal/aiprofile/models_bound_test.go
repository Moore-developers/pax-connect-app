package aiprofile

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type directoryTransport func(*http.Request) (*http.Response, error)

func (f directoryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestModelsResultBoundRejectsBeforeCatalogCommit(t *testing.T) {
	m, _ := claudeFixture(t)
	w := importClaude(t, m)
	before := w.Profiles[0]
	m.modelClient = &http.Client{Transport: directoryTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"real-model"}]}`))}, nil
	})}
	if _, err := m.RefreshModelsBounded(context.Background(), before.ID, 1); !errors.Is(err, Failure("result_too_large")) {
		t.Fatal(err)
	}
	w, err := m.Query(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if w.Profiles[0].Revision != before.Revision || w.Profiles[0].ModelsChecked != 0 || len(w.Profiles[0].Catalog) != 0 {
		t.Fatal("oversized result committed")
	}
}
