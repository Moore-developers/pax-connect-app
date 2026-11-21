package singbox

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/route"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type gateHandshakePacket struct {
	N.PacketConn
	failure error
	closed  int
}

func (p *gateHandshakePacket) Close() error {
	p.closed++
	return nil
}

func (p *gateHandshakePacket) HandshakeSuccess() error { return p.failure }

type gateHandshakeRemote struct {
	net.PacketConn
	closed int
}

func (p *gateHandshakeRemote) Close() error {
	p.closed++
	return nil
}

type gateHandshakeDialer struct{ remote net.PacketConn }

func (d gateHandshakeDialer) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, errors.New("unexpected stream dial")
}

func (d gateHandshakeDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return d.remote, nil
}

func TestGateUDPFailedHandshakeReleasesLeaseAndClosesBothSides(t *testing.T) {
	failure := errors.New("inbound handshake failed")
	inbound := &gateHandshakePacket{failure: failure}
	remote := &gateHandshakeRemote{}
	gate := &routeGate{}
	gate.configure(map[string]bool{"a": true})
	ctx, release, err := gate.acquire(context.Background(), "a", inbound.Close)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if gate.pending("a") != 1 {
		t.Fatal("fixture did not acquire its fixed route")
	}
	var callbacks int
	var callbackError error
	manager := route.NewConnectionManager(logger.NOP())
	manager.NewPacketConnection(ctx, gateHandshakeDialer{remote: remote}, inbound, adapter.InboundContext{
		Network: "udp", Destination: M.ParseSocksaddr("192.0.2.1:443"),
	}, func(err error) {
		callbacks++
		callbackError = err
		release()
	})
	if callbacks != 1 || !errors.Is(callbackError, failure) || inbound.closed != 1 || remote.closed != 1 {
		t.Fatalf("failed UDP handshake lost its close contract: callbacks=%d error=%v inbound=%d outbound=%d", callbacks, callbackError, inbound.closed, remote.closed)
	}
	if gate.pending("a") != 0 || ctx.Err() != context.Canceled {
		t.Fatal("ended UDP flow retained a lease and would prevent recovery")
	}
	gate.setBlocked("a", true)
	if inbound.closed != 1 {
		t.Fatal("fault cleanup found an already-closed UDP lease")
	}
}
