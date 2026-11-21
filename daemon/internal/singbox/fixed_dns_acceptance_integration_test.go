//go:build darwin && integration && with_quic && with_clash_api

package singbox

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	"pax-connect/daemon/internal/config"
)

// Each program verifies Darwin attribution for its own loopback sockets. Both
// programs feed DNS messages through the TUN callbacks to one TLS DNS peer that
// counts A, B and direct independently.
func TestFixedDNSRuntimeAcceptance(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	if other := os.Getenv("PAX_FIXED_DNS_ORDINARY_EXECUTABLE"); other != "" {
		verifyFixedDNSRuntime(t, executable, executable, other)
		return
	}
	app := copyFixedDNSClient(t, executable)
	t.Run("ordinary_process", func(t *testing.T) {
		verifyFixedDNSRuntime(t, executable, app, executable)
	})
	t.Run("bundled_app", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, app, "-test.run=^TestFixedDNSRuntimeAcceptance$", "-test.v")
		command.Env = []string{"PAX_FIXED_DNS_ORDINARY_EXECUTABLE=" + executable}
		output, err := command.CombinedOutput()
		t.Logf("\n%s", output)
		if err != nil {
			t.Fatal(err)
		}
	})
}

func verifyFixedDNSRuntime(t *testing.T, running, app, executable string) {
	t.Helper()
	ctx := context.Background()
	state := t.TempDir()
	fixture := &fixedDNSPeers{}
	resolver := httptest.NewUnstartedServer(http.HandlerFunc(fixture.resolve))
	resolver.EnableHTTP2 = true
	resolver.StartTLS()
	t.Cleanup(resolver.Close)
	a := fixture.route(t, "a", 0, resolver.Listener.Addr().String())
	b := fixture.route(t, "b", 1, resolver.Listener.Addr().String())
	appPath := filepath.Clean(filepath.Join(app, "..", "..", ".."))
	b.RoutingApplications = []config.RoutingApplication{{ID: "fixture-app", Path: appPath, Bundle: true}}
	library := config.NewRoutingLibrary()
	library.Packages[1].Enabled = false
	library.Packages[0].Rules = []config.RoutingEntry{
		{ID: "domain", Method: "domain_suffix", Target: "domain.fixture.test", Destination: "route:a", Enabled: true},
		{ID: "override", Method: "domain_suffix", Target: "override.fixture.test", Destination: "direct", Enabled: true},
		{ID: "app", Method: "application", Target: "fixture-app", Destination: "route:a", Enabled: true},
		{ID: "lower", Method: "domain_suffix", Target: "lower.fixture.test", Destination: "direct", Enabled: true},
	}
	b.Routing = &library
	profile := func(loaded bool) string {
		candidate := a
		candidate.TLSResolved.NeedsConfirmation = !loaded
		return fixedDNSIsolatedProfile(t, ctx, state, candidate, b, resolver, loaded)
	}
	runtime := NewCoreRuntime(ctx)
	t.Cleanup(runtime.Close)
	t.Cleanup(func() { _ = runtime.Clear() })
	apply := func(loaded bool) {
		t.Helper()
		if err := runtime.Apply(profile(loaded)); err != nil {
			t.Fatal(err)
		}
	}
	hot := func(loaded bool) {
		t.Helper()
		if err := runtime.UpdateRouting(ctx, profile(loaded)); err != nil {
			t.Fatal(err)
		}
	}
	query := func(network, client, name string, owner *adapter.ConnectionOwner, egress int) {
		t.Helper()
		source := "ordinary process"
		if owner != nil {
			source = "supplied system resolver owner"
		} else {
			if client == app {
				source = "fixture app"
			}
			if client != running {
				owner = &adapter.ConnectionOwner{ProcessPath: client}
				source += " (supplied owner)"
			} else {
				source += " (native Darwin owner)"
			}
		}
		before := fixture.counts()
		afterSequence := runtime.ReadObservations(ctx, "", 0, 1).HighWater
		rcode := runFixedDNSQuery(t, runtime, network, name, owner)
		after := fixture.counts()
		var delta, expected [3]int64
		for i := range delta {
			delta[i] = after[i] - before[i]
		}
		want := "REFUSED"
		if egress >= 0 {
			expected[egress] = 1
			want = "NOERROR"
		}
		t.Logf("%s %s from %s: %s (A/B/direct query deltas=%v)", network, name, source, rcode, delta)
		if owner == nil {
			var process, status string
			for _, entry := range runtime.ReadObservations(ctx, "", afterSequence, 32).Records {
				if f := entry.Flow; entry.Kind == "closed" && f != nil && f.Domain == name {
					process, status = f.Process, f.ProcessStatus
				}
			}
			if process != running {
				t.Fatalf("Darwin owner path=%q status=%s want %q", process, status, running)
			}
		}
		if rcode != want || delta != expected {
			t.Fatalf("%s %s: want %s and A/B/direct %v", network, name, want, expected)
		}
	}

	apply(true)
	for _, network := range []string{"tcp", "udp"} {
		query(network, executable, network+".domain.fixture.test", nil, 0)
		query(network, app, network+".shared.fixture.test", nil, 0)
		query(network, executable, network+".shared.fixture.test", nil, 1)
		query(network, app, network+".override.fixture.test", nil, 2)
		query(network, executable, network+".lower.fixture.test", nil, 2)
		query(network, executable, network+".lower.fixture.test", &adapter.ConnectionOwner{ProcessPath: "/usr/sbin/mDNSResponder"}, 1)
	}
	if running == app {
		query("udp6", app, "ipv6-unconnected.shared.fixture.test", nil, 0)
	}
	appRule := &library.Packages[0].Rules[2]
	appRule.Method, appRule.Target = "process", executable
	hot(true)
	query("udp", executable, "process.shared.fixture.test", nil, 0)
	query("udp", app, "process.shared.fixture.test", nil, 1)
	appRule.Method, appRule.Target, appRule.Destination = "application", "fixture-app", "global"
	geo := &library.Packages[1]
	geo.Enabled = true
	geo.GeoContent = []config.RoutingEntry{
		{ID: "cn", Method: "source", Target: "geosite-cn", Enabled: true, Match: []byte(`{"domain":["geo.fixture.test"]}`)},
	}
	hot(true)
	query("udp", app, "geo.fixture.test", nil, 1)
	query("udp", executable, "geo.fixture.test", nil, 2)
	appRule.Destination = "route:a"
	geo.Enabled = false

	apply(false)
	for _, network := range []string{"tcp", "udp"} {
		query(network, executable, "cold."+network+".domain.fixture.test", nil, -1)
		query(network, app, "cold."+network+".shared.fixture.test", nil, -1)
		query(network, executable, "cold."+network+".shared.fixture.test", nil, 1)
		query(network, app, "cold."+network+".override.fixture.test", nil, 2)
	}
	instance := runtime.current().Instance()
	domainRule := &library.Packages[0].Rules[0]
	domainRule.Destination = "global"
	hot(false)
	query("udp", executable, "hot.domain.fixture.test", nil, 1)
	domainRule.Destination = "route:a"
	hot(false)
	query("udp", executable, "hot.domain.fixture.test", nil, -1)
	appRule.Destination = "default"
	library.Packages[0].Rules[3].Destination = "route:a"
	hot(false)
	query("udp", app, "follow.lower.fixture.test", nil, 1)
	query("udp", executable, "follow.lower.fixture.test", nil, -1)
	query("udp", executable, "system-follow.lower.fixture.test", &adapter.ConnectionOwner{ProcessPath: "/usr/sbin/mDNSResponder"}, 1)
	if runtime.current().Instance() != instance {
		t.Fatal("updating DNS rules replaced the running instance")
	}
}

type fixedDNSPeers struct {
	connections sync.Map
	queries     [3]atomic.Int64
}

func (p *fixedDNSPeers) counts() [3]int64 {
	return [3]int64{p.queries[0].Load(), p.queries[1].Load(), p.queries[2].Load()}
}

func (p *fixedDNSPeers) resolve(w http.ResponseWriter, r *http.Request) {
	route := 2
	if path, ok := p.connections.Load(r.RemoteAddr); ok {
		route = path.(int)
	}
	p.queries[route].Add(1)
	raw, err := io.ReadAll(r.Body)
	var request, answer dns.Msg
	if err != nil || request.Unpack(raw) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	answer.SetReply(&request)
	answer.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: request.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.IPv4(192, 0, 2, byte(route+1))}}
	w.Header().Set("Content-Type", "application/dns-message")
	raw, _ = answer.Pack()
	_, _ = w.Write(raw)
}

func (p *fixedDNSPeers) route(t *testing.T, id string, index int, resolver string) config.RouteConfig {
	t.Helper()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != resolver {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		peer, err := net.DialTimeout("tcp", resolver, time.Second)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer peer.Close()
		endpoint := peer.LocalAddr().String()
		p.connections.Store(endpoint, index)
		defer p.connections.Delete(endpoint)
		client, tunnel, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer client.Close()
		if _, err = tunnel.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil || tunnel.Flush() != nil {
			return
		}
		go func() { _, _ = io.Copy(client, peer); _ = client.Close() }()
		_, _ = io.Copy(peer, tunnel)
	}))
	t.Cleanup(proxy.Close)
	port := proxy.Listener.Addr().(*net.TCPAddr).Port
	raw := fmt.Sprintf(`{"type":"http","server":"127.0.0.1","server_port":%d}`, port)
	route, err := config.RouteFromDocument([]byte(raw), id, id)
	if err != nil {
		t.Fatal(err)
	}
	return route
}

func fixedDNSIsolatedProfile(t *testing.T, ctx context.Context, state string, a, b config.RouteConfig, resolver *httptest.Server, loaded bool) string {
	t.Helper()
	rules := testRules(t)
	raw, err := CoreCatalogProfile(ctx, []config.RouteConfig{a, b}, b, rules, "127.0.0.1:17890")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, `"tag":"`+config.RuntimeTag(a)+`"`) != loaded {
		t.Fatalf("A load condition did not reach the generated core profile: loaded=%v", loaded)
	}
	validated, err := ValidateCoreOptions(ctx, []byte(raw), state, 17890)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(validated), &document); err != nil {
		t.Fatal(err)
	}
	delete(document, "inbounds")
	route := document["route"].(map[string]any)
	delete(route, "auto_detect_interface")
	for _, value := range route["rule_set"].([]any) {
		entry := value.(map[string]any)
		entry["path"] = filepath.Join(rules.Directory, filepath.Base(entry["path"].(string)))
	}
	cert := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: resolver.Certificate().Raw}))
	for _, value := range document["dns"].(map[string]any)["servers"].([]any) {
		server := value.(map[string]any)
		if server["type"] == "https" {
			server["server"] = "127.0.0.1"
			server["server_port"] = resolver.Listener.Addr().(*net.TCPAddr).Port
			server["tls"] = map[string]any{"enabled": true, "certificate": []string{cert}}
		}
	}
	isolated, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return string(isolated)
}

func copyFixedDNSClient(t *testing.T, source string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "Fixture.app", "Contents", "MacOS")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "client")
	if err := os.WriteFile(path, raw, 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func runFixedDNSQuery(t *testing.T, runtime *CoreRuntime, network, domain string, owner *adapter.ConnectionOwner) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(runtime.ctx, 4*time.Second)
	defer cancel()
	router := runtime.current().Instance().Box().Router()
	metadata := func(source, local net.Addr) adapter.InboundContext {
		return adapter.InboundContext{Inbound: "tun-in", InboundType: "tun", Network: network, Protocol: "dns",
			Source: M.SocksaddrFromNet(source), Destination: M.ParseSocksaddr("127.0.0.1:53"), OriginDestination: M.SocksaddrFromNet(local), ProcessInfo: owner}
	}
	loopback := netip.MustParseAddrPort("127.0.0.1:0")
	var address string
	switch network {
	case "tcp":
		listener, err := net.ListenTCP("tcp4", net.TCPAddrFromAddrPort(loopback))
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		address = listener.Addr().String()
		go func() {
			conn, err := listener.AcceptTCP()
			if err != nil {
				return
			}
			defer conn.Close()
			router.RouteConnectionEx(ctx, conn, metadata(conn.RemoteAddr(), conn.LocalAddr()), nil)
		}()
	case "udp", "udp6":
		listenNetwork := "udp4"
		if network == "udp6" {
			loopback = netip.AddrPortFrom(netip.IPv6Loopback(), 0)
			listenNetwork = "udp6"
		}
		listener, err := net.ListenUDP(listenNetwork, net.UDPAddrFromAddrPort(loopback))
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		address = listener.LocalAddr().String()
		go func() {
			payload := make([]byte, 4096)
			n, source, err := listener.ReadFromUDPAddrPort(payload)
			if err != nil {
				return
			}
			conn := fixedDNSPacketConn{listener, source}
			metadata := metadata(net.UDPAddrFromAddrPort(source), listener.LocalAddr())
			if network == "udp6" {
				destination := M.SocksaddrFromNet(listener.LocalAddr())
				router.RoutePacketConnectionEx(ctx, bufio.NewCachedPacketConn(conn, buf.As(payload[:n]), destination), metadata, nil)
				return
			}
			router.HijackDNSPacket(ctx, payload[:n], conn, metadata)
		}()
	}
	question := new(dns.Msg)
	question.SetQuestion(dns.Fqdn(domain), dns.TypeA)
	if network == "udp6" {
		return fixedDNSUnconnectedQuery(t, ctx, question, netip.MustParseAddrPort(address))
	}
	client := dns.Client{Net: network, Timeout: 2 * time.Second}
	answer, _, err := client.ExchangeContext(ctx, question, address)
	if err != nil {
		t.Fatal(err)
	}
	return dns.RcodeToString[answer.Rcode]
}

func fixedDNSUnconnectedQuery(t *testing.T, ctx context.Context, question *dns.Msg, destination netip.AddrPort) string {
	t.Helper()
	// A wildcard UDP socket exposes the Darwin dual-stack PCB used by sendto.
	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv6unspecified})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	deadline, _ := ctx.Deadline()
	if err := client.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	wire, err := question.Pack()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.WriteToUDPAddrPort(wire, destination); err != nil {
		t.Fatal(err)
	}
	answer, err := (&dns.Conn{Conn: client}).ReadMsg()
	if err != nil {
		t.Fatal(err)
	}
	return dns.RcodeToString[answer.Rcode]
}

type fixedDNSPacketConn struct {
	*net.UDPConn
	peer netip.AddrPort
}

func (w fixedDNSPacketConn) ReadPacket(packet *buf.Buffer) (M.Socksaddr, error) {
	n, _, err := w.ReadFromUDPAddrPort(packet.FreeBytes())
	packet.Truncate(n)
	return M.SocksaddrFromNet(w.LocalAddr()), err
}

func (w fixedDNSPacketConn) WritePacket(packet *buf.Buffer, _ M.Socksaddr) error {
	defer packet.Release()
	_, err := w.WriteToUDPAddrPort(packet.Bytes(), w.peer)
	return err
}
