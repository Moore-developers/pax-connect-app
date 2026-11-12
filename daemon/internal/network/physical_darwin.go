package network

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"net"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Physical is a verified native default interface, never a utun fallback.
type Physical struct {
	Name      string
	Index     int
	Epoch     string
	IPv6      bool
	IPv6State string
}

// PhysicalNetwork samples only physical network facts. TUN-specific route events
// do not invalidate this fingerprint; a gateway/address change does.
func PhysicalNetwork(ctx context.Context) (Physical, error) {
	output, err := exec.CommandContext(ctx, "/sbin/route", "-n", "get", "default").Output()
	if err != nil {
		return Physical{}, errors.New("physical_network_unavailable")
	}
	name := ParseRouteInterface(string(output))
	if name == "" || strings.HasPrefix(name, "utun") || name == "lo0" {
		return Physical{}, errors.New("physical_network_unavailable")
	}
	iface, err := net.InterfaceByName(name)
	if err != nil || iface.Flags&net.FlagUp == 0 {
		return Physical{}, errors.New("physical_network_unavailable")
	}
	addresses, err := iface.Addrs()
	if err != nil {
		return Physical{}, err
	}
	facts := []string{name}
	ipv6State := physicalIPv6State(ctx, name, addresses)
	ipv6 := ipv6State == "available"
	facts = append(facts, "ipv6:"+ipv6State)
	facts = append(facts, stableAddressFacts(addresses)...)
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "gateway:") {
			facts = append(facts, strings.TrimSpace(line))
		}
	}
	// Resolver-file rewrites do not change the physical egress path.
	sort.Strings(facts)
	hash := sha256.Sum256([]byte(strings.Join(facts, "\n")))
	return Physical{Name: name, Index: iface.Index, Epoch: hex.EncodeToString(hash[:]), IPv6: ipv6, IPv6State: ipv6State}, nil
}

// PhysicalClient binds both socket and DNS bootstrap to the same physical
// interface; environment/system HTTP proxies are ignored. Caller closes idles.
func PhysicalClient(p Physical, family string) (*http.Client, func()) {
	if family == "" && p.IPv4Only() {
		family = "4"
	}
	control := physicalControl(p.Index)
	dnsDial := &net.Dialer{Timeout: 2 * time.Second, Control: control}
	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		return dnsDial.DialContext(ctx, "udp4", "223.5.5.5:53")
	}}
	dial := &net.Dialer{Timeout: 3 * time.Second, Control: control, Resolver: resolver}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSHandshakeTimeout: 3 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return dial.DialContext(ctx, "tcp"+family, address)
	}}
	return &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, transport.CloseIdleConnections
}

func physicalControl(index int) func(string, string, syscall.RawConn) error {
	return func(network, address string, raw syscall.RawConn) error {
		var failure error
		err := raw.Control(func(fd uintptr) {
			level, opt := unix.IPPROTO_IP, unix.IP_BOUND_IF
			if strings.HasSuffix(network, "6") {
				level, opt = unix.IPPROTO_IPV6, unix.IPV6_BOUND_IF
			}
			failure = unix.SetsockoptInt(int(fd), level, opt, index)
		})
		if err != nil {
			return err
		}
		if failure != nil {
			return fmt.Errorf("%w: %v", ErrPhysicalBinding, failure)
		}
		return nil
	}
}

// DialPhysical binds a literal TCP target to a verified non-TUN interface.
func DialPhysical(ctx context.Context, p Physical, network, address string) (net.Conn, error) {
	if p.IPv4Only() {
		host, _, err := net.SplitHostPort(address)
		if err == nil {
			if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
				return nil, ErrIPv6Unavailable
			}
		}
		if network == "tcp" {
			network = "tcp4"
		}
		if network == "udp" {
			network = "udp4"
		}
	}
	if p.Index <= 0 || p.Name == "" || strings.HasPrefix(p.Name, "utun") {
		return nil, ErrPhysicalBinding
	}
	return (&net.Dialer{Timeout: 3 * time.Second, Control: physicalControl(p.Index)}).DialContext(ctx, network, address)
}

// ErrPhysicalBinding distinguishes interface setup failure from a remote TCP failure.
var ErrPhysicalBinding = errors.New("physical_binding_unavailable")

// ValidatePhysicalBinding verifies both socket options locally, without external traffic.
func ValidatePhysicalBinding(ctx context.Context, p Physical) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if p.Index <= 0 {
		return ErrPhysicalBinding
	}
	families := []int{unix.AF_INET}
	if p.IPv6 {
		families = append(families, unix.AF_INET6)
	}
	for _, family := range families {
		fd, err := unix.Socket(family, unix.SOCK_STREAM, 0)
		if err != nil {
			return ErrPhysicalBinding
		}
		level, opt := unix.IPPROTO_IP, unix.IP_BOUND_IF
		if family == unix.AF_INET6 {
			level, opt = unix.IPPROTO_IPV6, unix.IPV6_BOUND_IF
		}
		err = unix.SetsockoptInt(fd, level, opt, p.Index)
		_ = unix.Close(fd)
		if err != nil {
			return ErrPhysicalBinding
		}
	}
	return nil
}
