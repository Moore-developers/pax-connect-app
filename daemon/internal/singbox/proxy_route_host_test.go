//go:build hostproxy

package singbox

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/service"
	"pax-connect/daemon/internal/config"
)

// Explicit host acceptance only: actual native outbounds and loopback servers,
// no TUN, system proxy, external service, or user credentials.
func TestPurchasedProxyHost(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "proxy-path-ok") }))
	defer target.Close()
	for _, kind := range []string{"socks", "http", "https"} {
		for _, authenticated := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/auth=%t", kind, authenticated), func(t *testing.T) {
				address, roots, closeProxy := purchasedProxyServer(t, kind, authenticated)
				defer closeProxy()
				host, port, err := net.SplitHostPort(address)
				if err != nil {
					t.Fatal(err)
				}
				number, err := strconv.Atoi(port)
				if err != nil {
					t.Fatal(err)
				}
				typ := kind
				if typ == "https" {
					typ = "http"
				}
				document := map[string]any{"type": typ, "server": host, "server_port": number}
				if authenticated {
					document["username"], document["password"] = "fixture-user", "fixture:@password"
				}
				if kind == "https" {
					document["tls"] = map[string]any{"enabled": true, "server_name": "example.com"}
				}
				makeProfile := func() config.RouteConfig {
					raw, err := json.Marshal(document)
					if err != nil {
						t.Fatal(err)
					}
					p, err := config.RouteFromDocument(raw, "host-acceptance", "Host proxy")
					if err != nil {
						t.Fatal(err)
					}
					if err := ValidateRouteDocument(context.Background(), p.Document); err != nil {
						t.Fatal(err)
					}
					return p
				}
				runtime := NewMeasurements(nil)
				defer func() {
					if err := runtime.Close(context.Background()); err != nil {
						t.Error(err)
					}
				}()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if roots != nil {
					_, root, err := runtime.acquire(ctx)
					if err != nil {
						t.Fatal(err)
					}
					// Only this isolated test runtime trusts its generated loopback certificate.
					service.MustRegister[adapter.CertificateStore](root, hostProxyRoots{roots: roots})
					runtime.jobs.Done()
				}
				request := func(p config.RouteConfig) error {
					return runtime.WithRouteNetwork(ctx, p, func(dial func(context.Context, string, string) (net.Conn, error), _ func(context.Context, string) ([]netip.Addr, error)) error {
						transport := &http.Transport{DialContext: dial, Proxy: nil}
						defer transport.CloseIdleConnections()
						response, err := (&http.Client{Transport: transport, Timeout: 3 * time.Second}).Get(target.URL)
						if err != nil {
							return err
						}
						defer func() { _ = response.Body.Close() }()
						body, err := io.ReadAll(response.Body)
						if err != nil {
							return err
						}
						if string(body) != "proxy-path-ok" {
							return fmt.Errorf("wrong proxy response")
						}
						return nil
					})
				}
				if err := request(makeProfile()); err != nil {
					t.Fatal(err)
				}
				if authenticated {
					document["password"] = "wrong"
					err := request(makeProfile())
					if err == nil || nodeMeasurementFailure(err).Code != "proxy_authentication_failed" {
						t.Fatalf("wrong authentication classification: %v", err)
					}
					document["password"] = "fixture:@password"
				}
				if kind == "https" {
					document["tls"] = map[string]any{"enabled": true, "server_name": "wrong.example"}
					err := request(makeProfile())
					if err == nil || nodeMeasurementFailure(err).Code != "tls_certificate_invalid" {
						t.Fatalf("wrong certificate classification: %v", err)
					}
				}
			})
		}
	}
}

type hostProxyRoots struct {
	adapter.CertificateStore
	roots *x509.CertPool
}

func (s hostProxyRoots) Pool() *x509.CertPool   { return s.roots }
func (s hostProxyRoots) ExclusiveAnchors() bool { return true }

func TestPurchasedProxyHostUDP(t *testing.T) {
	echo, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = echo.Close() }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		buffer := make([]byte, 128)
		count, peer, err := echo.ReadFrom(buffer)
		if err == nil {
			_, _ = echo.WriteTo(buffer[:count], peer)
		}
	}()
	for _, kind := range []string{"http", "https", "socks"} {
		t.Run(kind, func(t *testing.T) {
			address, roots, closeProxy := purchasedProxyServer(t, kind, true)
			defer closeProxy()
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				t.Fatal(err)
			}
			number, err := strconv.Atoi(port)
			if err != nil {
				t.Fatal(err)
			}
			typ := kind
			if typ == "https" {
				typ = "http"
			}
			document := map[string]any{"type": typ, "server": host, "server_port": number, "username": "fixture-user", "password": "fixture:@password"}
			if kind == "https" {
				document["tls"] = map[string]any{"enabled": true, "server_name": "example.com"}
			}
			raw, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			profile, err := config.RouteFromDocument(raw, "udp-fixture", "UDP fixture")
			if err != nil {
				t.Fatal(err)
			}
			runtime := NewMeasurements(nil)
			defer func() {
				if err := runtime.Close(context.Background()); err != nil {
					t.Error(err)
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if roots != nil {
				_, root, err := runtime.acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				service.MustRegister[adapter.CertificateStore](root, hostProxyRoots{roots: roots})
				runtime.jobs.Done()
			}
			err = runtime.WithRouteNetwork(ctx, profile, func(dial func(context.Context, string, string) (net.Conn, error), _ func(context.Context, string) ([]netip.Addr, error)) error {
				conn, err := dial(ctx, "udp", echo.LocalAddr().String())
				if err != nil {
					return err
				}
				defer func() { _ = conn.Close() }()
				if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
					return err
				}
				if _, err := conn.Write([]byte("udp-path-ok")); err != nil {
					return err
				}
				buffer := make([]byte, 128)
				n, err := conn.Read(buffer)
				if err != nil {
					return err
				}
				if string(buffer[:n]) != "udp-path-ok" {
					return fmt.Errorf("wrong UDP response")
				}
				return nil
			})
			if kind == "socks" && err != nil {
				t.Fatal(err)
			}
			if kind != "socks" && err == nil {
				t.Fatal("HTTP proxy unexpectedly carried UDP")
			}
		})
	}
	_ = echo.Close()
	<-done
}

func purchasedProxyServer(t *testing.T, kind string, authenticated bool) (string, *x509.CertPool, func()) {
	t.Helper()
	if kind == "socks" {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
		inbound := map[string]any{"type": "socks", "listen": "127.0.0.1", "listen_port": port}
		if authenticated {
			inbound["users"] = []map[string]string{{"username": "fixture-user", "password": "fixture:@password"}}
		}
		raw, err := json.Marshal(map[string]any{"log": map[string]any{"disabled": true}, "inbounds": []any{inbound}, "outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}}})
		if err != nil {
			t.Fatal(err)
		}
		ctx := runtimeContext(context.Background())
		var options option.Options
		if err := options.UnmarshalJSONContext(ctx, raw); err != nil {
			t.Fatal(err)
		}
		server, err := box.New(box.Options{Context: ctx, Options: options})
		if err != nil {
			t.Fatal(err)
		}
		if err := server.Start(); err != nil {
			_ = server.Close()
			t.Fatal(err)
		}
		return fmt.Sprintf("127.0.0.1:%d", port), nil, func() {
			if err := server.Close(); err != nil {
				t.Error(err)
			}
		}
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("fixture-user:fixture:@password"))
		if authenticated && r.Header.Get("Proxy-Authorization") != expected {
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		if r.Method != http.MethodConnect {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		upstream, err := net.DialTimeout("tcp", r.Host, time.Second)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = upstream.Close()
			return
		}
		defer func() { _ = conn.Close(); _ = upstream.Close() }()
		if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
			return
		}
		done := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, conn); _ = upstream.Close(); close(done) }()
		_, _ = io.Copy(conn, upstream)
		_ = conn.Close()
		<-done
	})
	if kind == "https" {
		server := httptest.NewTLSServer(handler)
		roots := x509.NewCertPool()
		roots.AddCert(server.Certificate())
		return server.Listener.Addr().String(), roots, server.Close
	}
	server := httptest.NewServer(handler)
	return server.Listener.Addr().String(), nil, server.Close
}
