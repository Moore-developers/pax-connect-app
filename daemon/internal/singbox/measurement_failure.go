package singbox

import (
	"errors"
	"github.com/sagernet/ws"
	"pax-connect/daemon/internal/measurement"
	"strconv"
	"strings"
)

// Only the protocol adapter recognizes transport-library errors. The independent
// metric module receives a stable stage/code, never an upstream message.
func nodeMeasurementFailure(err error) *measurement.Failure {
	if cert := classifyCertificate(err); cert != nil {
		return &measurement.Failure{Code: "tls_certificate_invalid", Stage: "node_connection", Reason: cert.Reason}
	}
	var status ws.StatusError
	if errors.As(err, &status) {
		return &measurement.Failure{Code: "node_ws_handshake_failed", Stage: "node_transport", Reason: strconv.Itoa(int(status))}
	}
	if errors.Is(err, ws.ErrHandshakeBadStatus) || errors.Is(err, ws.ErrHandshakeBadUpgrade) || errors.Is(err, ws.ErrHandshakeBadConnection) || errors.Is(err, ws.ErrHandshakeBadSecAccept) {
		return &measurement.Failure{Code: "node_ws_handshake_failed", Stage: "node_transport"}
	}
	// The pinned SOCKS/HTTP clients expose these bounded handshake errors without typed sentinels.
	message := err.Error()
	if strings.Contains(message, "socks5: incorrect user name or password") || strings.HasSuffix(message, "authentication required") || strings.Contains(message, "socks5: unsupported auth method: 255") {
		return &measurement.Failure{Code: "proxy_authentication_failed", Stage: "node_authentication"}
	}
	if strings.Contains(message, "method not allowed") || strings.Contains(message, "unexpected status: 403 ") {
		return &measurement.Failure{Code: "proxy_target_rejected", Stage: "node_transport"}
	}
	return measurement.Classify(err, true)
}
