package subfetch

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestIconOnlyAnonymousSameOriginAndPublicTargets(t *testing.T) {
	calls := 0
	transport := roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/favicon.ico" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Fatal("request leaked state")
		}
		return response(200, "png"), nil
	})
	f := NewWithOptions(Options{Lookup: lookupAll(publicAddress), Transport: transport})
	if _, err := f.FetchIcon(context.Background(), "example.com"); err != nil || calls != 1 {
		t.Fatal(err)
	}
	for _, host := range []string{"127.0.0.1", "localhost"} {
		bad := NewWithOptions(Options{Lookup: lookupAll("127.0.0.1"), Transport: transport})
		if _, err := bad.FetchIcon(context.Background(), host); err == nil {
			t.Fatal(host)
		}
	}
	redirects := 0
	r := NewWithOptions(Options{Lookup: lookupAll(publicAddress), Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		redirects++
		p := response(302, "")
		p.Header.Set("Location", "https://other.example.com/favicon.ico")
		return p, nil
	})})
	if _, err := r.FetchIcon(context.Background(), "example.com"); err == nil || redirects != 1 {
		t.Fatal("cross-origin redirect accepted", err)
	}
	large := NewWithOptions(Options{Lookup: lookupAll(publicAddress), Transport: roundTrip(func(*http.Request) (*http.Response, error) { return response(200, strings.Repeat("x", (1<<20)+1)), nil })})
	if _, err := large.FetchIcon(context.Background(), "example.com"); err != ErrTooLarge {
		t.Fatal(err)
	}
}

type closingRoundTrip struct{ closed bool }

func (c *closingRoundTrip) RoundTrip(*http.Request) (*http.Response, error) {
	return response(200, "icon"), nil
}
func (c *closingRoundTrip) CloseIdleConnections() { c.closed = true }
func TestIconRequestReleasesPrivateTransport(t *testing.T) {
	transport := &closingRoundTrip{}
	f := NewWithOptions(Options{Lookup: lookupAll(publicAddress), Transport: transport})
	if _, err := f.FetchIcon(context.Background(), "example.com"); err != nil {
		t.Fatal(err)
	}
	if !transport.closed {
		t.Fatal("private transport leaked idle resources")
	}
}
