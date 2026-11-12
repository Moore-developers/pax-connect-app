//go:build integration

package singbox

import (
	"context"
	"fmt"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/experimental/paxobs"
	"github.com/sagernet/sing-box/route"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"net"
	"syscall"
	"testing"
)

type failingUDPDialer struct{ err error }

func (d failingUDPDialer) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, d.err
}
func (d failingUDPDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, d.err
}

type closingObservedPacket struct {
	N.PacketConn
	ctx context.Context
}

func (p closingObservedPacket) Close() error {
	paxobs.Emit(p.ctx, paxobs.Event{Stage: "closed"})
	return nil
}
func TestUDPFailureProducesStructuredHistoryEvidence(t *testing.T) {
	for _, udpConnect := range []bool{false, true} {
		for _, tc := range []struct {
			name string
			err  error
			code string
		}{
			{"refused", syscall.ECONNREFUSED, "connection_refused"},
			{"timeout", context.DeadlineExceeded, "connection_timeout"},
		} {
			t.Run(fmt.Sprintf("connect=%t/%s", udpConnect, tc.name), func(t *testing.T) {
				o := newCoreObservations()
				ctx := paxobs.WithConnection(paxobs.WithSink(context.Background(), o.capture))
				paxobs.Emit(ctx, paxobs.Event{Stage: "accepted", Inbound: "tun", Network: "udp", IP: "192.0.2.1", Port: 443})
				paxobs.Emit(ctx, paxobs.Event{Stage: "routed", Network: "udp", IP: "192.0.2.1", Outbound: "direct"})
				manager := route.NewConnectionManager(logger.NOP())
				manager.NewPacketConnection(ctx, failingUDPDialer{tc.err}, closingObservedPacket{ctx: ctx}, adapter.InboundContext{Network: "udp", UDPConnect: udpConnect, Destination: M.ParseSocksaddr("192.0.2.1:443")}, nil)
				failed, closed := -1, -1
				for i, e := range o.records {
					if e.Kind == "failed" {
						failed = i
						if e.Flow.Failure != tc.code {
							t.Fatalf("failure=%q want %q", e.Flow.Failure, tc.code)
						}
					}
					if e.Kind == "closed" {
						closed = i
						if e.Flow.Failure != tc.code {
							t.Errorf("closed lost failure: %q", e.Flow.Failure)
						}
					}
				}
				if failed < 0 || closed < 0 || failed >= closed {
					t.Fatalf("failed must precede closed: failed=%d closed=%d", failed, closed)
				}
			})
		}
	}
}
