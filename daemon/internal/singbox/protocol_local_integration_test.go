//go:build protocolintegration

package singbox

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"pax-connect/daemon/internal/measurement"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/option"
	"pax-connect/daemon/internal/config"
)

// Explicit loopback integration: real protocol encryption, no external host/root/TUN.
func TestLocalProtocolHandshakeMatrix(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer target.Close()
	previous := latencyTargets
	latencyTargets = []string{target.URL}
	defer func() { latencyTargets = previous }()
	type fixtureSpec struct {
		protocol, method, password, transport string
		tls                                   bool
	}
	fixtures := []fixtureSpec{{protocol: "trojan", password: "fixture-password", tls: true}}
	for _, protocol := range []string{"vmess", "vless", "trojan"} {
		for _, transport := range []string{"ws", "httpupgrade"} {
			fixtures = append(fixtures, fixtureSpec{protocol: protocol, password: "fixture-password", transport: transport, tls: true})
			if protocol == "vmess" {
				fixtures = append(fixtures, fixtureSpec{protocol: protocol, transport: transport})
			}
		}
	}
	fixtures = append(fixtures, fixtureSpec{protocol: "anytls", password: "fixture-password", tls: true})
	for _, method := range []string{"aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305", "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305"} {
		password := "fixture-password"
		if len(method) > 5 && method[:5] == "2022-" {
			size := 32
			if method == "2022-blake3-aes-128-gcm" {
				size = 16
			}
			key := make([]byte, size)
			if _, err := rand.Read(key); err != nil {
				t.Fatal(err)
			}
			password = base64.StdEncoding.EncodeToString(key)
		}
		fixtures = append(fixtures, fixtureSpec{protocol: "shadowsocks", method: method, password: password})
	}
	for _, fixture := range fixtures {
		protocol := fixture.protocol
		t.Run(protocol+"/"+fixture.method+"/"+fixture.transport+fmt.Sprint(fixture.tls), func(t *testing.T) {
			reservation, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := reservation.Addr().(*net.TCPAddr).Port
			if err = reservation.Close(); err != nil {
				t.Fatal(err)
			}
			inbound := map[string]any{"type": protocol, "tag": "server", "listen": "127.0.0.1", "listen_port": port}
			document := map[string]any{"type": protocol, "tag": "local", "server": "127.0.0.1", "server_port": port, "password": fixture.password}
			if protocol == "trojan" || protocol == "anytls" {
				inbound["users"] = []any{map[string]any{"name": "fixture", "password": fixture.password}}
			}
			if protocol == "vmess" || protocol == "vless" {
				delete(document, "password")
				document["uuid"] = "11111111-1111-1111-1111-111111111111"
				inbound["users"] = []any{map[string]any{"name": "fixture", "uuid": document["uuid"]}}
				if protocol == "vmess" {
					document["security"] = "auto"
				}
			}
			if fixture.transport != "" {
				transport := map[string]any{"type": fixture.transport, "path": "/fixture"}
				inbound["transport"] = transport
				clientTransport := map[string]any{"type": fixture.transport, "path": "/fixture"}
				if fixture.transport == "httpupgrade" {
					transport["host"] = "fixture.local"
					clientTransport["host"] = "fixture.local"
				} else {
					clientTransport["headers"] = map[string]string{"Host": "fixture.local"}
				}
				document["transport"] = clientTransport
			}
			if fixture.tls {
				cert, key := localCertificate(t)
				inbound["tls"] = map[string]any{"enabled": true, "certificate": []string{cert}, "key": []string{key}}
				document["tls"] = map[string]any{"enabled": true, "server_name": "localhost"}
			}
			if protocol == "shadowsocks" {
				inbound["method"] = fixture.method
				inbound["password"] = fixture.password
				inbound["network"] = "tcp"
				document["method"] = fixture.method
			}
			raw, err := json.Marshal(map[string]any{"log": map[string]any{"disabled": true}, "inbounds": []any{inbound}, "outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}}, "route": map[string]any{"final": "direct"}})
			if err != nil {
				t.Fatal(err)
			}
			ctx := runtimeContext(context.Background())
			var options option.Options
			if err = options.UnmarshalJSONContext(ctx, raw); err != nil {
				t.Fatal(err)
			}
			server, err := box.New(box.Options{Context: ctx, Options: options})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = server.Close() }()
			if err = server.Start(); err != nil {
				t.Fatal(err)
			}
			raw, err = json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			profile, err := config.RouteFromDocument(raw, "fixture", "Local")
			if err != nil {
				t.Fatal(err)
			}
			runtime := NewMeasurements(measurement.NewExitLookup())
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := runtime.Close(ctx); err != nil {
					t.Error(err)
				}
			})
			probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if fixture.tls {
				profile.TLSResolved = config.TLSPolicyView{Mode: config.TLSStrict}
				if _, err = runtime.TestRoute(probeCtx, profile); err == nil {
					t.Fatal("self-signed certificate accepted in strict mode")
				}
				// Test fixture supplies explicit consent; no product settings are changed.
				profile.TLSResolved = config.TLSPolicyView{Mode: config.TLSSkip, UserOverride: true}
			}
			if duration, err := runtime.TestRoute(probeCtx, profile); err != nil || duration <= 0 {
				t.Fatalf("real local handshake failed: %v", err)
			}

			diagnosis := runtime.DiagnoseRoute(probeCtx, profile)
			if diagnosis.NodeDuration <= 0 || diagnosis.HTTPSDuration <= 0 || diagnosis.NodeCode != "" || diagnosis.HTTPSCode != "" {
				t.Fatalf("diagnostic protocol path failed: %+v", diagnosis)
			}

			if fixture.transport == "httpupgrade" {
				document["transport"].(map[string]any)["host"] = "wrong.local"
				badRaw, err := json.Marshal(document)
				if err != nil {
					t.Fatal(err)
				}
				wrong, err := config.RouteFromDocument(badRaw, "fixture", "Wrong Host")
				if err != nil {
					t.Fatal(err)
				}
				wrong.TLSResolved = profile.TLSResolved
				if _, err = runtime.TestRoute(probeCtx, wrong); err == nil {
					t.Fatal("wrong HTTPUpgrade host accepted")
				}
			}

		})
	}
}
func localCertificate(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))
}
