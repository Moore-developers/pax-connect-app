//go:build protocolintegration && with_quic && with_utls && !with_grpc

package singbox

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/certificate"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/logger"
	"github.com/sagernet/sing/service"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/importers"
)

const vlessGRPCFixtureUUID = "11111111-1111-4111-8111-111111111111"

type vlessGRPCFixture struct {
	format, name, service, body string
	port                        int
}

func TestVLESSGRPCForwardsTLS(t *testing.T) {
	cert, key := localCertificate(t)
	fixtures := startVLESSGRPCFixtures(t, "tls", map[string]any{"enabled": true, "certificate": cert, "key": key}, "", "")
	runtime := newVLESSGRPCMeasurements(t, cert)
	target, hits := newVLESSGRPCEcho(t)
	testVLESSGRPCImports(t, fixtures, runtime, target, hits, "tls")

	profile := importVLESSGRPCFixture(t, fixtures[0], "tls")
	before := hits.Load()
	err := postVLESSGRPCEcho(t, newVLESSGRPCMeasurements(t, ""), profile, target.URL)
	var authorityError x509.UnknownAuthorityError
	if !errors.As(err, &authorityError) || hits.Load() != before {
		t.Fatalf("untrusted certificate should fail without reaching the origin: %v", err)
	}
	t.Log("untrusted certificate rejected; origin count unchanged")

	wrong := changedVLESSGRPCFixture(t, profile, func(document map[string]any) {
		document["tls"].(map[string]any)["server_name"] = "wrong.example.test"
	})
	err = postVLESSGRPCEcho(t, runtime, wrong, target.URL)
	var hostnameError x509.HostnameError
	if !errors.As(err, &hostnameError) || hits.Load() != before {
		t.Fatalf("wrong SNI should fail certificate hostname verification without reaching the origin: %v", err)
	}
	t.Log("wrong SNI rejected by certificate hostname verification; origin count unchanged")
}

func TestVLESSGRPCForwardsREALITY(t *testing.T) {
	fixtures := startVLESSGRPCRealityFixtures(t)
	runtime := newVLESSGRPCMeasurements(t, "")
	target, hits := newVLESSGRPCEcho(t)
	testVLESSGRPCImports(t, fixtures, runtime, target, hits, "reality")
}

func startVLESSGRPCRealityFixtures(t *testing.T) []vlessGRPCFixture {
	t.Helper()
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	handshake := httptest.NewUnstartedServer(http.NotFoundHandler())
	handshake.EnableHTTP2 = true
	handshake.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	handshake.StartTLS()
	t.Cleanup(handshake.Close)
	const shortID = "1058af"
	reality := map[string]any{
		"enabled":     true,
		"private_key": base64.RawURLEncoding.EncodeToString(key.Bytes()),
		"short_id":    shortID,
		"handshake": map[string]any{
			"server": "127.0.0.1", "server_port": handshake.Listener.Addr().(*net.TCPAddr).Port,
		},
	}
	tlsOptions := map[string]any{"enabled": true, "server_name": "localhost", "reality": reality}
	return startVLESSGRPCFixtures(t, "reality", tlsOptions, base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), shortID)
}

func startVLESSGRPCFixtures(t *testing.T, security string, tlsOptions map[string]any, publicKey, shortID string) []vlessGRPCFixture {
	t.Helper()
	prefix := "gRPC " + strings.ToUpper(security) + " "
	fixtures := []vlessGRPCFixture{
		{format: "uri", name: prefix + "Link", service: "Pax.Mixed/Link+β"},
		{format: "yaml", name: prefix + "YAML", service: "Pax.Mixed/YAML+β"},
		{format: "json", name: prefix + "JSON", service: "Pax.Mixed/JSON+β"},
	}
	for i := range fixtures {
		fixture := &fixtures[i]
		fixture.port = networkProtocolPort(t)
		startNetworkProtocolServer(t, map[string]any{
			"type": "vless", "listen": "127.0.0.1", "listen_port": fixture.port,
			"users":     []map[string]string{{"name": "fixture", "uuid": vlessGRPCFixtureUUID}},
			"tls":       tlsOptions,
			"transport": map[string]any{"type": "grpc", "service_name": fixture.service},
		})
	}
	link := fixtures[0]
	query := url.Values{"type": {"grpc"}, "security": {security}, "mode": {"gun"}, "sni": {"localhost"}, "serviceName": {link.service}}
	if security == "reality" {
		query.Set("fp", "chrome")
		query.Set("pbk", publicKey)
		query.Set("sid", shortID)
	}
	fixtures[0].body = (&url.URL{
		Scheme: "vless", User: url.User(vlessGRPCFixtureUUID), Host: fmt.Sprintf("127.0.0.1:%d", link.port),
		RawQuery: query.Encode(), Fragment: link.name,
	}).String()

	yaml := fixtures[1]
	yamlReality := ""
	if security == "reality" {
		yamlReality = fmt.Sprintf(", client-fingerprint: chrome, reality-opts: {public-key: %q, short-id: %q}", publicKey, shortID)
	}
	fixtures[1].body = fmt.Sprintf(`proxies:
  - {type: vless, name: %q, server: 127.0.0.1, port: %d, uuid: %s, tls: true, servername: localhost, alpn: [h2], network: grpc, grpc-opts: {grpc-service-name: %q}%s}
`, yaml.name, yaml.port, vlessGRPCFixtureUUID, yaml.service, yamlReality)

	native := fixtures[2]
	nativeTLS := map[string]any{"enabled": true, "server_name": "localhost", "alpn": []string{"h2"}}
	if security == "reality" {
		nativeTLS["utls"] = map[string]any{"enabled": true, "fingerprint": "chrome"}
		nativeTLS["reality"] = map[string]any{"enabled": true, "public_key": publicKey, "short_id": shortID}
	}
	document, err := json.Marshal(map[string]any{"outbounds": []map[string]any{{
		"type": "vless", "tag": native.name, "server": "127.0.0.1", "server_port": native.port,
		"uuid": vlessGRPCFixtureUUID, "tls": nativeTLS,
		"transport": map[string]any{"type": "grpc", "service_name": native.service},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	fixtures[2].body = string(document)
	return fixtures
}

func newVLESSGRPCMeasurements(t *testing.T, cert string) *Runtime {
	t.Helper()
	runtime := NewMeasurements(nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := runtime.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	if cert != "" {
		_, root, err := runtime.acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer runtime.jobs.Done()
		store, err := certificate.NewStore(root, logger.NOP(), option.CertificateOptions{Store: "none", Certificate: []string{cert}})
		if err != nil {
			t.Fatal(err)
		}
		service.MustRegister[adapter.CertificateStore](root, store)
	}
	return runtime
}

func importVLESSGRPCFixture(t *testing.T, fixture vlessGRPCFixture, security string) config.RouteConfig {
	t.Helper()
	batch, err := importers.ParseSubscription([]byte(fixture.body))
	if err != nil || len(batch.Items) != 1 {
		t.Fatalf("expected one import item, got %d: %v", len(batch.Items), err)
	}
	item := batch.Items[0]
	if item.Route == nil {
		t.Fatalf("VLESS gRPC rejected: %s", item.Error)
	}
	profile := *item.Route
	profile.ID = fixture.format
	profile.TLSResolved = config.SourceTLSPolicy(profile)
	if profile.ProtocolName != "vless" || profile.Transport != "grpc" || profile.Security != security || profile.Flow != "" || profile.TLSResolved.Mode != config.TLSStrict {
		t.Fatal("imported route did not preserve gRPC security and empty flow")
	}
	if security == "reality" && profile.Fingerprint != "chrome" {
		t.Fatal("imported REALITY route did not preserve the Chrome fingerprint")
	}
	return profile
}

func checkVLESSGRPCCoreProfile(t *testing.T, profile config.RouteConfig) {
	t.Helper()
	ctx := context.Background()
	if err := ValidateImportedRouteDocument(ctx, profile.Document); err != nil {
		t.Fatal(err)
	}
	port := networkProtocolPort(t)
	full, err := CoreProfile(ctx, profile, testRules(t), fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("build production core profile: %v", err)
	}
	if _, err := ValidateCoreOptions(ctx, []byte(full), t.TempDir(), port); err != nil {
		t.Fatalf("validate production core profile: %v", err)
	}
}

func testVLESSGRPCImports(t *testing.T, fixtures []vlessGRPCFixture, runtime *Runtime, target *httptest.Server, hits *atomic.Int32, security string) {
	t.Helper()
	for _, fixture := range fixtures {
		t.Run(fixture.format, func(t *testing.T) {
			profile := importVLESSGRPCFixture(t, fixture, security)
			checkVLESSGRPCCoreProfile(t, profile)
			assertVLESSGRPCEcho(t, runtime, profile, target.URL, hits)

			wrong := changedVLESSGRPCFixture(t, profile, func(document map[string]any) {
				document["transport"].(map[string]any)["service_name"] = strings.ToLower(fixture.service)
			})
			before := hits.Load()
			err := postVLESSGRPCEcho(t, runtime, wrong, target.URL)
			if err == nil || !strings.Contains(err.Error(), "unexpected status: 404") || hits.Load() != before {
				t.Fatalf("wrong service name should return gRPC-lite 404 without reaching the origin: %v", err)
			}
			t.Log("wrong service name returned gRPC-lite 404; origin count unchanged")
		})
	}
}

func changedVLESSGRPCFixture(t *testing.T, profile config.RouteConfig, change func(map[string]any)) config.RouteConfig {
	t.Helper()
	document, err := config.ParseDocument(profile.Document)
	if err != nil {
		t.Fatal(err)
	}
	change(document)
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := config.RouteFromDocument(raw, profile.ID, profile.Name)
	if err != nil {
		t.Fatal(err)
	}
	changed.TLSResolved = config.SourceTLSPolicy(changed)
	return changed
}

func newVLESSGRPCEcho(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		if request.Method != http.MethodPost || request.URL.Path != "/echo" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, request.Body, 32*1024))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(target.Close)
	return target, &hits
}

func assertVLESSGRPCEcho(t *testing.T, runtime *Runtime, profile config.RouteConfig, target string, hits *atomic.Int32) {
	t.Helper()
	before := hits.Load()
	if err := postVLESSGRPCEcho(t, runtime, profile, target); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != before+1 {
		t.Fatalf("expected exactly one additional origin request, got %d", hits.Load()-before)
	}
	t.Log("HTTP POST returned 200 and exact random body; origin received one request")
}

func postVLESSGRPCEcho(t *testing.T, runtime *Runtime, profile config.RouteConfig, target string) error {
	t.Helper()
	payload := make([]byte, 16*1024)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	return runtime.WithRouteNetwork(ctx, profile, func(dial func(context.Context, string, string) (net.Conn, error), _ func(context.Context, string) ([]netip.Addr, error)) error {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, target+"/echo", bytes.NewReader(payload))
		if err != nil {
			return err
		}
		request.Close = true
		conn, err := dial(ctx, "tcp", request.URL.Host)
		if err != nil {
			return err
		}
		defer conn.Close()
		writeErr := request.Write(conn)
		// gRPC-lite delivers TLS/HTTP2 failures on Read; the HTTP write can
		// observe the request pipe closing before that error is available.
		response, err := http.ReadResponse(bufio.NewReader(conn), request)
		if err != nil {
			return errors.Join(writeErr, err)
		}
		defer response.Body.Close()
		if writeErr != nil {
			return writeErr
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, int64(len(payload)+1)))
		if err != nil {
			return err
		}
		if response.StatusCode != http.StatusOK || !bytes.Equal(body, payload) {
			return fmt.Errorf("origin HTTP %d or mismatched echo", response.StatusCode)
		}
		return nil
	})
}
