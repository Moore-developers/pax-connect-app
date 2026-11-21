package environment

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserPresentationRejectsContractChanges(t *testing.T) {
	for _, p := range []*Presentation{
		{Locale: "fr", Messages: map[string]string{}},
		{Locale: "en", Messages: map[string]string{"unknown": "text"}},
		{Locale: "en", Messages: map[string]string{"browser.countdown": "No count"}},
		{Locale: "en", Messages: map[string]string{"browser.countdown": "{password}"}},
		{Locale: "en", Messages: map[string]string{"browser.title": strings.Repeat("x", 2049)}},
		{Locale: "en", Messages: map[string]string{"browser.title": "\x00"}},
	} {
		if ValidatePresentation(p) == nil {
			t.Fatalf("invalid presentation accepted: %#v", p)
		}
	}
	p := &Presentation{Locale: "en", Messages: map[string]string{"browser.countdown": "{seconds} seconds · {{literal}}"}}
	if err := ValidatePresentation(p); err != nil {
		t.Fatal(err)
	}
	copy := copyPresentation(p)
	p.Messages["browser.countdown"] = "changed later"
	if copy.Messages["browser.countdown"] != "{seconds} seconds · {{literal}}" || copy.Messages["browser.title"] == "" {
		t.Fatal("snapshot did not freeze and merge words")
	}
}

func TestBrowserPresentationEndpointDoesNotModifyFactsOrInterpretMarkup(t *testing.T) {
	text := "<script>alert(1)</script>"
	s := &Session{
		origin: "http://127.0.0.1:1234", path: "/fixture/",
		state:        Snapshot{ID: "nonce", BrowserURL: "http://127.0.0.1:1234/fixture/"},
		presentation: copyPresentation(&Presentation{Locale: "en", Messages: map[string]string{"browser.title": text}}),
	}
	req := httptest.NewRequest(http.MethodGet, s.origin+s.path+"presentation", nil)
	w := httptest.NewRecorder()
	s.serve(context.Background(), w, req)
	var result Presentation
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Messages["browser.title"] != text {
		t.Fatalf("incorrect text response: %v %s", err, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "<script>") || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		t.Fatal("response was not JSON-escaped data")
	}
	if s.state.Mac.Complete || s.state.Browser.Complete || s.state.Complete {
		t.Fatal("reading display words changed probe facts")
	}
	wrong := httptest.NewRequest(http.MethodGet, "http://evil.invalid/fixture/presentation", nil)
	w = httptest.NewRecorder()
	s.serve(context.Background(), w, wrong)
	if w.Code != http.StatusNotFound {
		t.Fatal("foreign host accepted")
	}
}

func TestBrowserPageRendersSessionLanguageBeforeScriptsLoad(t *testing.T) {
	for _, tc := range []struct {
		locale, heading string
	}{
		{"en", "Checking the browser environment…"},
		{"zh-Hans", "正在检测浏览器环境…"},
	} {
		s := &Session{
			origin: "http://127.0.0.1:1234", path: "/fixture/",
			state: Snapshot{ID: "fixture-nonce"},
			presentation: copyPresentation(&Presentation{
				Locale:   tc.locale,
				Messages: map[string]string{"browser.title": "<b>Custom title</b>"},
			}),
		}
		w := httptest.NewRecorder()
		s.serve(context.Background(), w, httptest.NewRequest(http.MethodGet, s.origin+s.path, nil))
		for _, expected := range []string{
			`lang="` + tc.locale + `" data-pax-locale="` + tc.locale + `"`,
			`<title>&lt;b&gt;Custom title&lt;/b&gt;</title>`,
			tc.heading,
			`nonce="fixture-nonce" src="browser.js"`,
		} {
			if !strings.Contains(w.Body.String(), expected) {
				t.Errorf("%s HTML missing %q", tc.locale, expected)
			}
		}
		if strings.Contains(w.Body.String(), "<b>Custom title</b>") {
			t.Fatal("custom title interpreted as markup")
		}
	}
}
