package ipc

import (
	"context"
	"fmt"
	"net"
	"os"
)

// ProtocolVersion is shared by the daemon and its local Go adapters.
const ProtocolVersion uint32 = 49

// DialUser verifies the server UID before transmitting any product request.
func DialUser(ctx context.Context, path string) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	uid, err := peerUID(conn)
	if err != nil || uid != os.Geteuid() {
		_ = conn.Close()
		return nil, fmt.Errorf("peer_identity_invalid")
	}
	return conn, nil
}
