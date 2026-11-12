//go:build integration

package singbox

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/miekg/dns"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/protocol/socks"
	"github.com/sagernet/sing/protocol/socks/socks5"
)

func qualityRuntime(t *testing.T) (*CoreRuntime, int) {
	t.Helper()
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &dns.Server{PacketConn: packet, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
		response := new(dns.Msg)
		response.SetReply(q)
		if q.Question[0].Name == "missing.test." {
			response.Rcode = dns.RcodeNameError
		} else if q.Question[0].Qtype == dns.TypeA {
			response.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 1}, A: net.ParseIP("127.0.0.1")}}
		}
		_ = w.WriteMsg(response)
	})}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() { _ = server.Shutdown(); _ = packet.Close() })
	port := freeLoopbackPort(t)
	r := NewCoreRuntime(context.Background())
	t.Cleanup(r.Close)
	profile := fmt.Sprintf(`{"log":{"level":"error"},"experimental":{"cache_file":{"path":%q}},"dns":{"servers":[{"type":"udp","tag":"fixture","server":"127.0.0.1","server_port":%d}],"final":"fixture","strategy":"ipv4_only"},"inbounds":[{"type":"mixed","tag":"mixed","listen":"127.0.0.1","listen_port":%d}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"find_process":true,"default_domain_resolver":"fixture","rules":[{"port":53,"action":"hijack-dns"}],"final":"direct"}}`, filepath.Join(t.TempDir(), "cache.db"), packet.LocalAddr().(*net.UDPAddr).Port, port)
	if err = r.Apply(profile); err != nil {
		t.Fatal(err)
	}
	return r, port
}

func TestObservedDomainDirectIPIsActualDialPeer(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer target.Close()
	r, port := qualityRuntime(t)
	proxy, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	_, targetPort, _ := net.SplitHostPort(target.Listener.Addr().String())
	response, err := (&http.Client{Transport: transport, Timeout: 3 * time.Second}).Get("http://fixture.test:" + targetPort)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	_ = r.Clear()
	seenPhase := false
	for _, event := range r.ReadObservations(context.Background(), "", 0, 256).Records {
		if event.Kind == "phase" && event.Flow != nil && event.Flow.Phase == "connecting" {
			seenPhase = true
		}
		if f := event.Flow; f != nil && f.Domain == "fixture.test" && event.Kind == "closed" {
			if f.IP != "127.0.0.1" || f.IPSource != "connected" || !seenPhase {
				t.Fatalf("FQDN direct connection lost actual peer IP: %+v", f)
			}
			return
		}
	}
	t.Fatal("no closed FQDN connection")
}

func TestObservedDNSQueriesHaveIndependentFactsAndResults(t *testing.T) {
	r, port := qualityRuntime(t)
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err = socks.ClientHandshake5(conn, socks5.CommandConnect, M.ParseSocksaddr("192.0.2.53:53"), "", ""); err != nil {
		t.Fatal(err)
	}
	client := &dns.Conn{Conn: conn}
	for _, name := range []string{"fixture.test.", "missing.test."} {
		q := new(dns.Msg)
		q.SetQuestion(name, dns.TypeA)
		if err = client.WriteMsg(q); err != nil {
			t.Fatal(err)
		}
		if _, err = client.ReadMsg(); err != nil {
			t.Fatal(err)
		}
	}
	_ = conn.Close()
	_ = r.Clear()
	keys := map[string]bool{}
	results := map[string]string{}
	for _, event := range r.ReadObservations(context.Background(), "", 0, 256).Records {
		if f := event.Flow; f != nil && event.Kind == "closed" {
			if f.Purpose != "dns" || f.Direction != "dns" || f.DNS == nil || f.DNS.QueryType != "A" || !f.ResponseKnown || f.IP != "" || f.ProcessStatus == "" {
				t.Fatalf("DNS bypass left misleading access facts: %+v", f)
			}
			keys[f.Key] = true
			results[f.Domain] = f.DNS.Result
			if f.Domain == "fixture.test" && (len(f.DNS.Answers) != 1 || f.DNS.Answers[0] != "127.0.0.1") {
				t.Fatal("missing DNS answer", f.DNS)
			}
			if f.Domain == "missing.test" && f.Failure != "dns_not_found" {
				t.Fatal("lost NXDOMAIN cause", f)
			}
		}
	}
	if len(keys) != 2 || results["fixture.test"] != "NOERROR" || results["missing.test"] != "NXDOMAIN" {
		t.Fatal("queries were merged or not recorded", keys, results)
	}
}

func TestObservedUDPResponseHasTimingOrigin(t *testing.T) {
	echo, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		b := make([]byte, 64)
		n, a, e := echo.ReadFrom(b)
		if e == nil {
			_, _ = echo.WriteTo(b[:n], a)
		}
	}()
	r, port := qualityRuntime(t)
	control, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	_ = control.SetDeadline(time.Now().Add(3 * time.Second))
	response, err := socks.ClientHandshake5(control, socks5.CommandUDPAssociate, M.SocksaddrFrom(netip.MustParseAddr("127.0.0.1"), 0), "", "")
	if err != nil {
		t.Fatal(err)
	}
	relay := response.Bind.UDPAddr()
	relay.IP = net.ParseIP("127.0.0.1")
	conn, err := net.DialUDP("udp", nil, relay)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	packet := socks.NewAssociatePacketConn(conn, M.Socksaddr{}, control)
	if _, err = packet.WriteTo([]byte("echo"), echo.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 64)
	if _, _, err = packet.ReadFrom(data); err != nil {
		t.Fatal(err)
	}
	_ = control.Close()
	_ = r.Clear()
	for _, event := range r.ReadObservations(context.Background(), "", 0, 256).Records {
		if f := event.Flow; f != nil && f.Transport == "udp" && f.ResponseKnown {
			return
		}
	}
	t.Fatal("UDP echo succeeded but no first-response timing was recorded")
}

func TestObservedDNSUDPQueriesKeepIndependentResults(t *testing.T) {
	r, port := qualityRuntime(t)
	control, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	_ = control.SetDeadline(time.Now().Add(3 * time.Second))
	response, err := socks.ClientHandshake5(control, socks5.CommandUDPAssociate, M.SocksaddrFrom(netip.MustParseAddr("127.0.0.1"), 0), "", "")
	if err != nil {
		t.Fatal(err)
	}
	relay := response.Bind.UDPAddr()
	relay.IP = net.ParseIP("127.0.0.1")
	conn, err := net.DialUDP("udp", nil, relay)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	packet := socks.NewAssociatePacketConn(conn, M.Socksaddr{}, control)
	for _, name := range []string{"fixture.test.", "missing.test."} {
		q := new(dns.Msg)
		q.SetQuestion(name, dns.TypeA)
		raw, e := q.Pack()
		if e != nil {
			t.Fatal(e)
		}
		if _, e = packet.WriteTo(raw, &net.UDPAddr{IP: net.ParseIP("192.0.2.53"), Port: 53}); e != nil {
			t.Fatal(e)
		}
		b := make([]byte, 4096)
		n, _, e := packet.ReadFrom(b)
		if e != nil {
			t.Fatal(e)
		}
		answer := new(dns.Msg)
		if e = answer.Unpack(b[:n]); e != nil {
			t.Fatal(e)
		}
	}
	_ = control.Close()
	_ = r.Clear()
	keys := map[string]bool{}
	for _, event := range r.ReadObservations(context.Background(), "", 0, 256).Records {
		if f := event.Flow; f != nil && event.Kind == "closed" {
			if f.Purpose != "dns" || f.Transport != "udp" || f.DNS == nil || f.DNS.Result == "" || !f.ResponseKnown {
				t.Fatal("UDP DNS was stored as an incomplete access", f)
			}
			keys[f.Key] = true
		}
	}
	if len(keys) != 2 {
		t.Fatal("UDP DNS questions merged into one record", keys)
	}
}
