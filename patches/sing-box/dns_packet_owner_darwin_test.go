//go:build darwin

package process

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

func TestPaxDNSPacketOwner(t *testing.T) {
	source := netip.MustParseAddrPort("127.0.0.1:43210")
	destination := netip.MustParseAddrPort("127.0.0.1:53")
	bound := darwinConnectionEntry{localAddr: source.Addr(), localPort: source.Port(), remoteAddr: netip.IPv4Unspecified(), pid: 10, uid: 500}
	connected := bound
	connected.remoteAddr, connected.remotePort = destination.Addr(), destination.Port()
	otherPeer := connected
	otherPeer.pid, otherPeer.remotePort = 20, 54
	wildcard := bound
	wildcard.pid, wildcard.localAddr = 30, netip.IPv4Unspecified()
	unknownPID := wildcard
	unknownPID.pid = 0
	for _, tc := range []struct {
		name    string
		entries []darwinConnectionEntry
		wantPID uint32
	}{
		{"matching connected socket", []darwinConnectionEntry{otherPeer, connected}, 10},
		{"unconnected socket", []darwinConnectionEntry{otherPeer, bound}, 10},
		{"wildcard socket", []darwinConnectionEntry{otherPeer, wildcard}, 30},
		{"same process owns both sockets", []darwinConnectionEntry{bound, connected}, 10},
		{"different processes share the wire tuple", []darwinConnectionEntry{connected, wildcard}, 0},
		{"different processes share the unconnected port", []darwinConnectionEntry{bound, wildcard}, 0},
		{"one possible sender has no PID", []darwinConnectionEntry{connected, unknownPID}, 0},
		{"only a different destination", []darwinConnectionEntry{otherPeer}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner, err := matchDarwinDNSPacketOwner(tc.entries, source, destination)
			if (tc.wantPID == 0 && err != ErrNotFound) || (tc.wantPID != 0 && (err != nil || owner.pid != tc.wantPID)) {
				t.Fatalf("PID %d, error %v, want PID %d or unknown if zero", owner.pid, err, tc.wantPID)
			}
		})
	}
}

func TestPaxDNSPacketOwnerDualStack(t *testing.T) {
	source := netip.MustParseAddrPort("[::1]:43210")
	destination := netip.MustParseAddrPort("[::1]:53")
	pcb := make([]byte, darwinXsocketOffset)
	pcb[darwinXinpcbVFlag] = 0x3 // A kernel wildcard socket enabled for both families.
	binary.BigEndian.PutUint16(pcb[darwinXinpcbLocalPort:], source.Port())
	socket := make([]byte, structSize-darwinXsocketOffset)
	binary.NativeEndian.PutUint32(socket[darwinXsocketUID:], 500)
	binary.NativeEndian.PutUint32(socket[darwinXsocketLastPID:], 10)
	legacy, ok := parseDarwinConnectionEntry(pcb, socket, false)
	if !ok || legacy.localAddr != netip.IPv4Unspecified() {
		t.Fatalf("ordinary lookup changed its IPv4 projection: %v", legacy)
	}
	wildcard, ok := parseDarwinConnectionEntry(pcb, socket, true)
	if !ok || wildcard.localAddr != netip.IPv6Unspecified() {
		t.Fatalf("IPv6 DNS missed a dual-stack owner: %v", wildcard)
	}
	owner, err := matchDarwinDNSPacketOwner([]darwinConnectionEntry{wildcard}, source, destination)
	if err != nil || owner.pid != 10 {
		t.Fatalf("IPv6 DNS sender: PID %d, error %v", owner.pid, err)
	}
	exact := darwinConnectionEntry{localAddr: source.Addr(), localPort: source.Port(), remoteAddr: destination.Addr(), remotePort: destination.Port(), pid: 20, uid: 500}
	if _, err := matchDarwinDNSPacketOwner([]darwinConnectionEntry{wildcard, exact}, source, destination); err != ErrNotFound {
		t.Fatalf("IPv6 dual-stack wildcard can also have sent the packet: %v", err)
	}
}
