package measurement

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"
)

type fixtureClient func(*http.Request) (*http.Response, error)

func (f fixtureClient) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestClassifyPreservesStageAndSanitizesValues(t *testing.T) {
	for _, tc := range []struct {
		err  error
		node bool
		code string
	}{
		{&net.DNSError{Name: "private.secret", Err: "sensitive"}, true, "node_dns_failed"},
		{&net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, true, "node_connection_refused"},
		{syscall.ECONNRESET, true, "node_connection_reset"},
		{syscall.EHOSTUNREACH, true, "node_network_unreachable"},
		{io.EOF, true, "node_connection_closed"},
		{io.EOF, false, "target_connection_closed"},
		{x509.HostnameError{Host: "sensitive"}, true, "tls_certificate_invalid"},
		{x509.HostnameError{Host: "sensitive"}, false, "target_tls_failed"},
	} {
		f := Classify(tc.err, tc.node)
		if f.Code != tc.code || strings.Contains(f.Error(), "sensitive") || strings.Contains(f.Error(), "private") {
			t.Fatal(f)
		}
	}
}

func TestLatencyRejectsHTTPFailureAndPreservesNodeError(t *testing.T) {
	for _, tc := range []struct {
		status int
		err    error
		want   string
	}{
		{status: 200}, {status: 403, want: "target_http_failed"},
		{err: &Failure{Code: "node_connection_refused", Stage: "node_connection"}, want: "node_connection_refused"},
	} {
		client := fixtureClient(func(*http.Request) (*http.Response, error) {
			if tc.err != nil {
				return nil, tc.err
			}
			return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(""))}, nil
		})
		duration, err := Latency(context.Background(), client, []string{"https://example.test/"})
		if tc.want == "" {
			if err != nil || duration <= 0 {
				t.Fatal(duration, err)
			}
		} else {
			var f *Failure
			if !errors.As(err, &f) || f.Code != tc.want || duration != 0 {
				t.Fatal(duration, err)
			}
			if tc.status == 403 && f.Reason != "403" {
				t.Fatal("HTTP status missing", f)
			}
		}
	}
}

func TestExitDeadlineDoesNotCancelIndependentLatencySample(t *testing.T) {
	latency := fixtureClient(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	exit := fixtureClient(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })
	root, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	measured, err := Latency(root, latency, []string{"https://example.test/"})
	if err != nil {
		t.Fatal(err)
	}
	bounded, stop := context.WithTimeout(root, 5*time.Millisecond)
	defer stop()
	ip, code := Exit(bounded, exit)
	if measured <= 0 || ip != "" || code != "exit_timeout" || root.Err() != nil {
		t.Fatal(measured, ip, code, root.Err())
	}
}

func TestCapacityM1AndHardLimit(t *testing.T) {
	if got := Recommended(8, 16*1024*1024*1024); got.Initial != 20 || got.Ceiling != 32 || got.HardLimit != 100 {
		t.Fatal(got)
	}
	if got := Recommended(64, 256*1024*1024*1024); got.Ceiling != 100 {
		t.Fatal(got)
	}
}
