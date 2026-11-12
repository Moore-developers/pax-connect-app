package singbox

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"
)

type privacyTLSOutbound struct {
	adapter.Outbound
	primary, backup string
	mu              sync.Mutex
	destinations    []string
}

func (o *privacyTLSOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	o.mu.Lock()
	o.destinations = append(o.destinations, destination.String())
	o.mu.Unlock()
	address := o.primary
	if destination.Addr.String() == "119.29.29.29" {
		address = o.backup
	}
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

type privacyTLSManager struct {
	adapter.OutboundManager
	outbound *privacyTLSOutbound
}

func (m privacyTLSManager) Outbound(tag string) (adapter.Outbound, bool) {
	return m.outbound, tag == "fixture-proxy"
}
func TestNativeEncryptedDNSFallbackVerifiesTLSAndPreservesDetour(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Pax fixture"}, DNSNames: []string{"dns.alidns.com", "doh.pub"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, _ := x509.MarshalECPrivateKey(key)
	pair, err := tls.X509KeyPair(certPEM, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	makeServer := func(handler http.HandlerFunc) *httptest.Server {
		server := httptest.NewUnstartedServer(handler)
		server.EnableHTTP2 = true
		server.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
		server.StartTLS()
		t.Cleanup(server.Close)
		return server
	}
	primary := makeServer(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	backup := makeServer(func(w http.ResponseWriter, r *http.Request) {
		raw, e := io.ReadAll(r.Body)
		if e != nil {
			t.Error(e)
			return
		}
		q := new(dns.Msg)
		if e = q.Unpack(raw); e != nil {
			t.Error(e)
			return
		}
		response := new(dns.Msg)
		response.SetReply(q)
		response.Rcode = dns.RcodeNameError
		wire, _ := response.Pack()
		w.Header().Set("Content-Type", "application/dns-message")
		_, _ = w.Write(wire)
	})
	out := &privacyTLSOutbound{primary: primary.Listener.Addr().String(), backup: backup.Listener.Addr().String()}
	ctx := runtimeContext(context.Background())
	ctx = service.ContextWith[adapter.OutboundManager](ctx, privacyTLSManager{outbound: out})
	for _, trusted := range []bool{true, false} {
		server := encryptedDNS("fixture-dns", "223.5.5.5", "dns.alidns.com", "fixture-proxy")
		if trusted {
			server["tls"].(map[string]any)["certificate"] = []string{string(certPEM)}
		}
		raw, _ := json.Marshal(server)
		var opts option.DNSServerOptions
		if e := opts.UnmarshalJSONContext(ctx, raw); e != nil {
			t.Fatal(e)
		}
		logger := log.NewNOPFactory().Logger()
		pool, e := newEncryptedPool(ctx, logger, "fixture-dns", *opts.Options.(*option.RemoteHTTPSDNSServerOptions))
		if e != nil {
			t.Fatal(e)
		}
		scope := adapter.NewScope(ctx, logger)
		if e = pool.Start(adapter.StartStateStart, scope); e != nil {
			t.Fatal(e)
		}
		q := new(dns.Msg)
		q.SetQuestion("fixture.test.", dns.TypeA)
		response, e := pool.Exchange(ctx, q)
		if trusted && (e != nil || response.Rcode != dns.RcodeNameError) {
			t.Fatal("native HTTPS backup failed", e)
		}
		if !trusted && e == nil {
			t.Fatal("untrusted DNS TLS certificate accepted")
		}
		if e = scope.Close(); e != nil {
			t.Fatal(e)
		}
	}
	out.mu.Lock()
	defer out.mu.Unlock()
	if len(out.destinations) != 4 || out.destinations[0] != "223.5.5.5:443" || out.destinations[1] != "119.29.29.29:443" {
		t.Fatal("fallback escaped controlled outbound", out.destinations)
	}
}
