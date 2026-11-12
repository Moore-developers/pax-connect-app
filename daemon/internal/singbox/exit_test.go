package singbox

import (
	"context"
	"net"
	"syscall"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/ws"
	"pax-connect/daemon/internal/measurement"
)

type failingExitOutbound struct {
	adapter.Outbound
	failure error
}

func (o failingExitOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, o.failure
}

func TestExitNativeDialRetainsSanitizedNodeStage(t *testing.T) {
	for _, fixture := range []struct {
		failure error
		code    string
	}{
		{syscall.ECONNRESET, "node_connection_reset"},
		{ws.StatusError(403), "node_ws_handshake_failed"},
	} {
		value, code, _ := measureExit(context.Background(), failingExitOutbound{failure: fixture.failure}, measurement.NewExitLookup(), "fixture")
		if value != nil || code != fixture.code {
			t.Fatal("native node failure became exit service error", code, fixture.code)
		}
	}
}
