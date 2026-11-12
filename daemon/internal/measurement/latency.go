package measurement

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"syscall"
	"time"
)

// Client is the narrow HTTP boundary used by all three independent metrics.
type Client interface {
	Do(*http.Request) (*http.Response, error)
}

// DownloadResult contains only valid received bytes and elapsed request time.
type DownloadResult struct {
	Bytes          uint64
	Duration       time.Duration
	BytesPerSecond float64
	Setup          time.Duration
	End            string
}

// Failure carries sanitized stage evidence. Raw upstream messages are not retained.
type Failure struct {
	Code   string
	Stage  string
	Reason string
	cause  error
}

func (f *Failure) Error() string { return f.Code }
func (f *Failure) Unwrap() error { return f.cause }

// Classify distinguishes node-dial failures from target-HTTPS failures.
func Classify(err error, node bool) *Failure {
	var known *Failure
	if errors.As(err, &known) {
		return known
	}
	prefix, stage := "target", "target_https"
	if node {
		prefix, stage = "node", "node_connection"
	}
	code, reason := prefix+"_connection_failed", ""
	var dns *net.DNSError
	var network net.Error
	var hostname x509.HostnameError
	var trust x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var verification *tls.CertificateVerificationError
	switch {
	case errors.As(err, &hostname):
		code, reason = prefix+"_tls_failed", "hostname_mismatch"
	case errors.As(err, &trust):
		code, reason = prefix+"_tls_failed", "untrusted"
	case errors.As(err, &invalid):
		code, reason = prefix+"_tls_failed", "noncompliant"
		if invalid.Reason == x509.Expired {
			reason = "expired"
		}
	case errors.As(err, &verification):
		code, reason = prefix+"_tls_failed", "noncompliant"
	case errors.As(err, &dns):
		code = prefix + "_dns_failed"
	case errors.Is(err, syscall.ECONNREFUSED):
		code = prefix + "_connection_refused"
	case errors.Is(err, syscall.ECONNRESET):
		code = prefix + "_connection_reset"
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		code = prefix + "_network_unreachable"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		code = prefix + "_connection_closed"
	case errors.As(err, &network) && network.Timeout():
		code = prefix + "_connection_timeout"
	}
	if node && code == "node_tls_failed" {
		code = "tls_certificate_invalid"
	}
	return &Failure{Code: code, Stage: stage, Reason: reason, cause: err}
}

// Latency measures request start to the first usable HTTPS response headers.
// Fallback waiting remains part of the existing product measurement contract.
func Latency(ctx context.Context, client Client, targets []string) (time.Duration, error) {
	var failure *Failure
	started := time.Now()
	for _, target := range targets {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return 0, &Failure{Code: "test_target_invalid", Stage: "target_https"}
		}
		response, err := client.Do(req)
		if err == nil {
			duration := time.Since(started)
			_ = response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 400 {
				return duration, nil
			}
			failure = &Failure{Code: "target_http_failed", Stage: "target_https", Reason: strconv.Itoa(response.StatusCode)}
		} else {
			failure = Classify(err, false)
			if failure.Code == "tls_certificate_invalid" {
				return 0, failure
			}
		}
	}
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	if failure == nil {
		failure = &Failure{Code: "test_target_invalid", Stage: "target_https"}
	}
	return 0, failure
}
