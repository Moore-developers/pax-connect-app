package singbox

import (
	"fmt"
	"github.com/sagernet/ws"
	"testing"
)

func TestNodeWebSocketHandshakeKeepsStatusWithoutUpstreamText(t *testing.T) {
	failure := nodeMeasurementFailure(fmt.Errorf("private header: %w", ws.StatusError(403)))
	if failure.Code != "node_ws_handshake_failed" || failure.Reason != "403" || failure.Error() != "node_ws_handshake_failed" {
		t.Fatal(failure)
	}
}
