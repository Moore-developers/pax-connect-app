package main

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"pax-connect/daemon/internal/network"
)

// Opt-in host proof; the ordinary suite stays offline and never toggles a VPN.
func TestCFPhysicalEgressHost(t *testing.T) {
	if os.Getenv("PAX_CF_EGRESS_HOST") != "1" {
		t.Skip("explicit physical-network host check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	physical, err := network.PhysicalNetwork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := network.DialPhysical(ctx, physical, "tcp4", "172.64.79.122:443")
	if err != nil {
		t.Fatal("physical TCP unavailable")
	}
	defer func() { _ = conn.Close() }()
	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		t.Fatal("not TCP")
	}
	raw, err := tcp.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	bound := 0
	var controlErr error
	if err = raw.Control(func(fd uintptr) { bound, controlErr = unix.GetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_BOUND_IF) }); err != nil || controlErr != nil || bound != physical.Index {
		t.Fatal("socket not bound to physical interface")
	}
	iface, err := net.InterfaceByIndex(bound)
	if err != nil {
		t.Fatal(err)
	}
	addresses, err := iface.Addrs()
	if err != nil {
		t.Fatal(err)
	}
	local := tcp.LocalAddr().(*net.TCPAddr).IP
	matched := false
	for _, a := range addresses {
		ip, _, _ := net.ParseCIDR(a.String())
		if ip.Equal(local) {
			matched = true
		}
	}
	if !matched {
		t.Fatal("local socket address is not physical interface address")
	}
	probe := cfPhysicalProbe(physical, nil)
	if err = probe.Validate(ctx); err != nil {
		t.Fatal("physical binding invalid")
	}
	duration, err := probe.TCP(ctx, "172.64.79.122")
	if err != nil || duration <= 0 {
		t.Fatal("CF physical probe failed")
	}
	t.Logf("interface=%s bound_index=%d physical_source_match=true CF_TCP_ms=%d", physical.Name, bound, duration.Milliseconds())
}
