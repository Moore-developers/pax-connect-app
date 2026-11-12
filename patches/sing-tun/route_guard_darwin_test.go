package tun

import (
	"errors"
	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
	"net"
	"net/netip"
	"syscall"
	"testing"
)

// Regression for the nil InterfaceFinder panic: sing-box's tun inbound never
// sets Options.InterfaceFinder and leaves InterfaceScope false, so setRoutes
// must resolve the utun index through the paxInterfaceByName seam instead of
// calling the (nil) InterfaceFinder, and must go through paxWriteRoute so the
// add path is testable without root.
func TestPaxSetRoutesResolvesInterfaceWithoutInterfaceFinderAndRecordsRealIndex(t *testing.T) {
	originalInterface, originalWrite := paxInterfaceByName, paxWriteRoute
	defer func() {
		paxInterfaceByName = originalInterface
		paxWriteRoute = originalWrite
	}()

	native := &NativeTun{}
	native.options.Name = "utun9"
	native.options.AutoRoute = true
	native.options.Inet4Address = []netip.Prefix{netip.MustParsePrefix("198.18.0.1/15")}
	// InterfaceFinder stays nil and InterfaceScope stays false, matching
	// sing-box's protocol/tun inbound construction (the crashing production path).

	t.Run("lookup failure returns error, does not panic", func(t *testing.T) {
		paxInterfaceByName = func(string) (*net.Interface, error) { return nil, errors.New("no such interface") }
		paxWriteRoute = func(int, bool, int, netip.Prefix, netip.Addr) error {
			t.Fatal("route write attempted despite lookup failure")
			return nil
		}
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("setRoutes panicked: %v", recovered)
			}
		}()
		if err := native.setRoutes(); err == nil {
			t.Fatal("expected error from failed interface lookup")
		}
	})

	t.Run("resolves utun index and records route without InterfaceFinder", func(t *testing.T) {
		native.paxRoutes = nil
		native.routeSet = false
		paxInterfaceByName = func(name string) (*net.Interface, error) {
			if name != "utun9" {
				t.Fatalf("looked up wrong interface: %s", name)
			}
			return &net.Interface{Index: 9}, nil
		}
		var wroteIndex int
		paxWriteRoute = func(kind int, scope bool, index int, destination netip.Prefix, gateway netip.Addr) error {
			wroteIndex = index
			if kind != unix.RTM_ADD {
				t.Fatalf("unexpected route message kind: %d", kind)
			}
			if scope {
				t.Fatal("InterfaceScope was false, route must not be interface-scoped")
			}
			return nil
		}
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("setRoutes panicked: %v", recovered)
			}
		}()
		if err := native.setRoutes(); err != nil {
			t.Fatalf("setRoutes failed: %v", err)
		}
		if wroteIndex != 9 {
			t.Fatalf("route write used index=%d, want the real utun index 9", wroteIndex)
		}
		if len(native.paxRoutes) == 0 {
			t.Fatal("expected route to be recorded for ownership tracking")
		}
		for _, owned := range native.paxRoutes {
			if owned.interfaceIndex != 9 {
				t.Fatalf("recorded route interfaceIndex=%d, want 9", owned.interfaceIndex)
			}
		}
	})
}

func TestPaxCleanupPreservesForeignReplacementAndOnlyDeletesAcknowledgedOwnedRoutes(t *testing.T) {
	originalInterface, originalRead, originalWrite := paxInterfaceByName, paxReadRoutes, paxWriteRoute
	defer func() {
		paxInterfaceByName = originalInterface
		paxReadRoutes = originalRead
		paxWriteRoute = originalWrite
	}()
	paxInterfaceByName = func(string) (*net.Interface, error) { return &net.Interface{Index: 7}, nil }
	prefix := netip.MustParsePrefix("0.0.0.0/1")
	for _, scenario := range []string{"owned", "foreign-interface", "foreign-gateway", "foreign-mask", "foreign-scope", "observation-failed", "not-installed"} {
		t.Run(scenario, func(t *testing.T) {
			native := &NativeTun{}
			native.options.Name = "utun7"
			row := &route.RouteMessage{Index: 7, Addrs: make([]route.Addr, syscall.RTAX_MAX)}
			row.Addrs[syscall.RTAX_DST] = &route.Inet4Addr{IP: [4]byte{0, 0, 0, 0}}
			row.Addrs[syscall.RTAX_NETMASK] = &route.Inet4Addr{IP: [4]byte{128, 0, 0, 0}}
			row.Addrs[syscall.RTAX_GATEWAY] = &route.Inet4Addr{IP: [4]byte{172, 29, 231, 2}}
			if scenario == "foreign-interface" {
				row.Index = 8
			}
			if scenario == "foreign-gateway" {
				row.Addrs[syscall.RTAX_GATEWAY] = &route.Inet4Addr{IP: [4]byte{172, 19, 0, 2}}
			}
			if scenario == "foreign-mask" {
				row.Addrs[syscall.RTAX_NETMASK] = &route.Inet4Addr{IP: [4]byte{192, 0, 0, 0}}
			}
			if scenario == "foreign-scope" {
				row.Flags = unix.RTF_IFSCOPE
			}
			if scenario != "not-installed" {
				native.paxRecordRoute(prefix, netip.MustParseAddr("172.29.231.2"), 7)
			}
			paxReadRoutes = func() ([]route.Message, error) {
				if scenario == "observation-failed" {
					return nil, errors.New("unavailable")
				}
				return []route.Message{row}, nil
			}
			writes := 0
			paxWriteRoute = func(kind int, scope bool, index int, destination netip.Prefix, gateway netip.Addr) error {
				writes++
				if kind != unix.RTM_DELETE || index != 7 || destination.String() != "0.0.0.0/1" || gateway.String() != "172.29.231.2" {
					t.Fatal("cleanup broadened ownership")
				}
				return nil
			}
			err := native.unsetRoutes()
			if scenario == "observation-failed" && err == nil {
				t.Fatal("failed observation hidden")
			}
			expected := 0
			if scenario == "owned" {
				expected = 1
			}
			if writes != expected {
				t.Fatalf("route writes=%d, want %d", writes, expected)
			}
		})
	}
}
