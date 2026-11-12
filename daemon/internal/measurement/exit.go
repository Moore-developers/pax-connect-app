package measurement

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"syscall"
)

func Exit(ctx context.Context, client Client) (string, string) {
	last := "exit_query_failed"
	for _, target := range []string{"https://www.cloudflare.com/cdn-cgi/trace", "https://1.1.1.1/cdn-cgi/trace"} {
		ip, code := queryExitTarget(ctx, client, target)
		if code == "" || code == "exit_response_invalid" {
			return ip, code
		}
		last = code
		if ctx.Err() != nil {
			break
		}
	}
	return "", last
}
func queryExitTarget(ctx context.Context, client Client, target string) (string, string) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", "exit_query_failed"
	}
	response, err := client.Do(request)
	if err != nil {
		return "", exitFailure(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", "exit_http_failed"
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 16385))
	if err != nil {
		if ctx.Err() != nil {
			return "", exitFailure(ctx.Err())
		}
		return "", exitFailure(err)
	}
	if len(body) > 16384 {
		return "", "exit_response_invalid"
	}
	return parseExit(body)
}
func parseExit(body []byte) (string, string) {
	found := ""
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, "ip=") {
			continue
		}
		if found != "" {
			return "", "exit_response_invalid"
		}
		address, err := netip.ParseAddr(strings.TrimSpace(strings.TrimPrefix(line, "ip=")))
		if err != nil || !address.IsGlobalUnicast() || address.IsPrivate() {
			return "", "exit_response_invalid"
		}
		found = address.Unmap().String()
	}
	if found == "" {
		return "", "exit_response_invalid"
	}
	return found, ""
}

func exitFailure(err error) string {
	var known *Failure
	if errors.As(err, &known) {
		return known.Code
	}
	if errors.Is(err, context.Canceled) {
		return "operation_cancelled"
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENETUNREACH) || errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return Classify(err, false).Code
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return "exit_dns_failed"
	}
	var certificate x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	if errors.As(err, &certificate) || errors.As(err, &hostname) || errors.As(err, &invalid) {
		return "exit_tls_failed"
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return "exit_timeout"
	}
	return "exit_query_failed"
}

// exitFailureReason returns the sanitized evidence of a typed failure, such as a WS handshake status.
func exitFailureReason(err error) string {
	var known *Failure
	if errors.As(err, &known) {
		return known.Reason
	}
	return ""
}
