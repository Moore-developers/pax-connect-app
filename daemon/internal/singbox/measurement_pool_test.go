package singbox

import (
	"context"
	"errors"
	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
	"net"
	"sync"
	"testing"
	"time"
)

type stalledEarlyConn struct {
	net.Conn
	closed chan struct{}
	once   sync.Once
}

func (c *stalledEarlyConn) NeedHandshake() bool       { return true }
func (c *stalledEarlyConn) Write([]byte) (int, error) { <-c.closed; return 0, net.ErrClosed }
func (c *stalledEarlyConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

type earlyOutbound struct {
	adapter.Outbound
	connection net.Conn
}

func (o earlyOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return o.connection, nil
}
func TestMeasurementEarlyHandshakeHonorsRequestDeadline(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = b.Close() }()
	connection := &stalledEarlyConn{Conn: a, closed: make(chan struct{})}
	defer func() { _ = connection.Close() }()
	outbound := measurementOutbound{Outbound: earlyOutbound{connection: connection}, lifetime: context.Background()}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := outbound.DialContext(ctx, "tcp", M.ParseSocksaddr("fixture.test:443"))
		result <- err
	}()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("wrong cancellation cause", err)
		}
	case <-time.After(time.Second):
		t.Fatal("early handshake outlived request budget")
	}
}
