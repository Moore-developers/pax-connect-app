package network

import (
	"net"
	"reflect"
	"testing"
)

func addr(t *testing.T, cidr string) net.Addr {
	t.Helper()
	ip, n, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatal(err)
	}
	n.IP = ip
	return n
}

func TestStableAddressFactsIgnoreIPv6PrivacyRotation(t *testing.T) {
	before := []net.Addr{addr(t, "192.168.1.7/24"), addr(t, "fe80::1/64"),
		addr(t, "240e:351:1bd7:b700:1048:7e4b:ec57:acaf/64")}
	after := []net.Addr{addr(t, "192.168.1.7/24"), addr(t, "fe80::1/64"),
		addr(t, "240e:351:1bd7:b700:aaaa:bbbb:cccc:dddd/64")}
	if a, b := stableAddressFacts(before), stableAddressFacts(after); !reflect.DeepEqual(a, b) {
		t.Fatalf("rotation changed facts: %v vs %v", a, b)
	}
	moved := []net.Addr{addr(t, "192.168.2.7/24")}
	if reflect.DeepEqual(stableAddressFacts(before), stableAddressFacts(moved)) {
		t.Fatal("IPv4 change must change facts")
	}
}
