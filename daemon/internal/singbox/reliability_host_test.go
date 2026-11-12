//go:build hostdiagnostic

package singbox

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"path/filepath"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
	"pax-connect/daemon/internal/network"
	"pax-connect/daemon/internal/telemetry"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Explicit read-only diagnostic: fixed public targets via three saved routes.
// No private document/name/IP/URL, query response or credential reaches output.
func TestHostMeasurementFailureStages(t *testing.T) {
	root := os.Getenv("PAX_DIAGNOSTIC_DIRECTORY")
	if root == "" {
		t.Skip("explicit local profile permission required")
	}
	catalog, exists, err := config.NewCatalogStore(filepath.Join(root, "routes.json"), uint32(os.Getuid())).Load()
	if err != nil || !exists {
		t.Fatal("catalog unavailable")
	}
	saved, err := config.NewMeasurementStore(filepath.Join(root, "route-measurements.json"), uint32(os.Getuid())).Load()
	if err != nil {
		t.Fatal("summaries unavailable")
	}
	sort.Slice(catalog.Routes, func(i, j int) bool { return catalog.Routes[i].Config.ID < catalog.Routes[j].Config.ID })
	selected := map[string]config.RouteConfig{}
	for _, r := range catalog.Routes {
		if r.Chain != nil || r.Hidden {
			continue
		}
		rec := saved.Records[r.Config.ID]
		label := ""
		if rec.Latency != nil && rec.Latency.Success && rec.Latency.ExitError == "exit_timeout" {
			label = "exit-timeout"
		}
		if rec.Download != nil && rec.Download.Error == "download_timeout" && rec.Download.Bytes > 256*1024 {
			label = "download-useful-timeout"
		}
		if rec.Download != nil && rec.Download.Error == "node_connection_reset" {
			label = "node-reset"
		}
		if label != "" {
			if _, ok := selected[label]; !ok {
				selected[label] = r.Config
			}
		}
	}
	diagnostics := filepath.Join(t.TempDir(), "provider-stages.jsonl")
	telemetry.ConfigureDiagnostics(diagnostics)
	defer telemetry.ConfigureDiagnostics("")
	runtime := NewMeasurements(measurement.NewExitLookup())
	defer func() { _ = runtime.Close(context.Background()) }()
	tlsStore := config.NewTLSPolicyStore(filepath.Join(root, "tls-policies.json"), uint32(os.Getuid()))
	for _, label := range []string{"exit-timeout", "download-useful-timeout", "node-reset"} {
		p, ok := selected[label]
		if !ok {
			continue
		}
		t.Run(label, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 28*time.Second)
			defer cancel()
			p.TLSResolved, err = tlsStore.Resolve(ctx, p)
			if err != nil || p.TLSResolved.NeedsConfirmation {
				t.Fatal("TLS policy unavailable")
			}
			physical, e := network.PhysicalNetwork(ctx)
			if e != nil {
				t.Fatal("physical network unavailable")
			}
			var doc map[string]any
			_ = json.Unmarshal(config.DocumentForRoute(p), &doc)
			doc["bind_interface"] = physical.Name
			p.Document, _ = json.Marshal(doc)
			err = runtime.withMeasurementOutbound(ctx, p, func(ctx context.Context, out adapter.Outbound) error {
				transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, kind, address string) (net.Conn, error) {
					return out.DialContext(ctx, kind, M.ParseSocksaddr(address))
				}}
				defer transport.CloseIdleConnections()
				client := &http.Client{Transport: transport, Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
				for _, target := range []struct{ name, url string }{{"ipwho", "https://ipwho.is/"}, {"ipsb", "https://api.ip.sb/geoip"}, {"cf", "https://www.cloudflare.com/cdn-cgi/trace"}} {
					if ctx.Err() != nil {
						break
					}
					start := time.Now()
					var mu sync.Mutex
					var connected, tlsDone, headers time.Duration
					trace := &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { mu.Lock(); connected = time.Since(start); mu.Unlock() }, TLSHandshakeDone: func(tls.ConnectionState, error) { mu.Lock(); tlsDone = time.Since(start); mu.Unlock() }, GotFirstResponseByte: func() { mu.Lock(); headers = time.Since(start); mu.Unlock() }}
					req, _ := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), "GET", target.url, nil)
					req.Header.Set("User-Agent", "PaxConnect/1.0")
					response, e := client.Do(req)
					status := 0
					if e == nil {
						status = response.StatusCode
						_, e = io.Copy(io.Discard, io.LimitReader(response.Body, 16385))
						_ = response.Body.Close()
					}
					code := ""
					if e != nil {
						code = measurement.Classify(e, false).Code
					}
					mu.Lock()
					t.Logf("target=%s status=%d code=%s total_ms=%d connect_ms=%d tls_ms=%d headers_ms=%d", target.name, status, code, time.Since(start).Milliseconds(), connected.Milliseconds(), tlsDone.Milliseconds(), headers.Milliseconds())
					mu.Unlock()
				}
				if label == "exit-timeout" {
					bounded, stop := context.WithTimeout(ctx, 4*time.Second)
					start := time.Now()
					value, code, _ := measureExit(bounded, out, runtime.exits, "host-diagnostic")
					stop()
					t.Logf("production_exit code=%s success=%t duration_ms=%d", code, value != nil, time.Since(start).Milliseconds())
					raw, _ := os.ReadFile(diagnostics)
					for _, line := range strings.Split(string(raw), "\n") {
						if line != "" {
							var d telemetry.Diagnostic
							if json.Unmarshal([]byte(line), &d) == nil {
								t.Logf("stage=%s provider=%s code=%s status=%s elapsed_ms=%d", d.Stage, d.Phase, d.Code, d.Reason, d.DurationMS)
							}
						}
					}
				}
				if label != "exit-timeout" {
					bounded, stop := context.WithTimeout(ctx, 12*time.Second)
					defer stop()
					value, e := diagnosticDownload(bounded, out)
					code := ""
					if e != nil {
						code = measurement.Classify(e, false).Code
					}
					t.Logf("download code=%s end=%s setup_ms=%d body_ms=%d bytes=%d", code, value.End, value.Setup.Milliseconds(), value.Duration.Milliseconds(), value.Bytes)
				}
				return nil
			})
			if err != nil {
				t.Log("runtime_code=test_runtime_failed")
			}
		})
	}
}
func diagnosticDownload(ctx context.Context, out adapter.Outbound) (measurement.DownloadResult, error) {
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, kind, address string) (net.Conn, error) {
		return out.DialContext(ctx, kind, M.ParseSocksaddr(address))
	}}
	defer transport.CloseIdleConnections()
	return measurement.Download(ctx, &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, measurement.DownloadBudget)
}
