package measurement

import (
	"context"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

type exitTransport func(*http.Request) (*http.Response, error)

func (f exitTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestExitResponseIsBoundedAndValidated(t *testing.T) {
	for _, test := range []struct{ body, ip, code string }{
		{"ip=8.8.8.8\nloc=US\n", "8.8.8.8", ""},
		{"ip=2606:4700:4700::1111\n", "2606:4700:4700::1111", ""},
		{"ip=192.168.1.2\n", "", "exit_response_invalid"},
		{"ip=8.8.8.8\nip=1.1.1.1\n", "", "exit_response_invalid"},
		{strings.Repeat("a", 16385), "", "exit_response_invalid"},
	} {
		client := &http.Client{Transport: exitTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != "https://www.cloudflare.com/cdn-cgi/trace" {
				t.Fatal("wrong measurement target")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(test.body))}, nil
		})}
		ip, code := Exit(context.Background(), client)
		if ip != test.ip || code != test.code {
			t.Fatal(ip, code)
		}
	}
}

type deadlineBody struct{ ctx context.Context }

func (b deadlineBody) Read([]byte) (int, error) { <-b.ctx.Done(); return 0, b.ctx.Err() }
func (b deadlineBody) Close() error             { return nil }
func TestExitResponseBodyDeadlineRemainsTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	client := fixtureClient(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: deadlineBody{ctx: r.Context()}}, nil
	})
	_, code := Exit(ctx, client)
	if code != "exit_timeout" {
		t.Fatal(code)
	}
}

func TestExitFailureClassificationIsSanitized(t *testing.T) {
	for _, test := range []struct {
		err  error
		code string
	}{
		{&net.DNSError{Err: "secret host", Name: "private.test"}, "exit_dns_failed"},
		{x509.HostnameError{Host: "private.test"}, "exit_tls_failed"},
		{context.DeadlineExceeded, "exit_timeout"},
	} {
		if got := exitFailure(test.err); got != test.code {
			t.Fatal(got)
		}
	}
}

func TestExitHasBoundedStrictHTTPSFallback(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: exitTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			if r.URL.Host != "www.cloudflare.com" {
				t.Fatal("unexpected primary")
			}
			return nil, &net.DNSError{Name: "fixture", Err: "failed"}
		}
		if calls != 2 || r.URL.String() != "https://1.1.1.1/cdn-cgi/trace" {
			t.Fatal("unexpected fallback")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ip=8.8.8.8\n"))}, nil
	})}
	ip, code := Exit(context.Background(), client)
	if ip != "8.8.8.8" || code != "" || calls != 2 {
		t.Fatal(ip, code, calls)
	}
}
