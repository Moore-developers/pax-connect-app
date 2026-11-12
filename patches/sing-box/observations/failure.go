package paxobs

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"strings"
	"syscall"
)

// FailureCode classifies observed errors without treating unknown text as proof.
func FailureCode(err error) string {
	if err == nil {
		return ""
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return "dns_failed"
	}
	var invalid x509.CertificateInvalidError
	var authority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	if errors.As(err, &invalid) || errors.As(err, &authority) || errors.As(err, &hostname) {
		return "certificate_invalid"
	}
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkError) && networkError.Timeout() {
		return "connection_timeout"
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "connection_refused"
	}
	if errors.Is(err, syscall.ENETUNREACH) || errors.Is(err, syscall.EHOSTUNREACH) {
		return "network_unreachable"
	}
	// Upstream TLS libraries may wrap certificate errors as bounded strings.
	if strings.Contains(err.Error(), "x509: certificate") {
		return "certificate_invalid"
	}
	return "connection_failed"
}
