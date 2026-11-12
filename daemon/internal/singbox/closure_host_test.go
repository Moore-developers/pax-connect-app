//go:build hostdiagnostic

package singbox

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
	"pax-connect/daemon/internal/network"
)

// Explicit host diagnostic. Only fixed public targets, controlled stages and error
// types are printed; route names/documents, IPs and response contents never are.
func TestHostClosedResponseStages(t *testing.T) {
	directory := os.Getenv("PAX_DIAGNOSTIC_DIRECTORY")
	if directory == "" {
		t.Skip("explicit host diagnostic only")
	}
	catalog, exists, err := config.NewCatalogStore(filepath.Join(directory, "routes.json"), uint32(os.Getuid())).Load()
	if err != nil || !exists {
		t.Fatal("catalog unavailable")
	}
	saved, err := config.NewMeasurementStore(filepath.Join(directory, "route-measurements.json"), uint32(os.Getuid())).Load()
	if err != nil {
		t.Fatal("samples unavailable")
	}
	sort.Slice(catalog.Routes, func(i, j int) bool { return catalog.Routes[i].Config.ID < catalog.Routes[j].Config.ID })
	selected := map[string]config.RouteConfig{}
	for _, r := range catalog.Routes {
		if r.Chain != nil || r.Hidden {
			continue
		}
		record := saved.Records[r.Config.ID]
		if record.Download != nil && record.Download.Error == "target_connection_closed" {
			if _, ok := selected["body-closed"]; !ok {
				selected["body-closed"] = r.Config
			}
		}
		if record.Latency != nil && record.Latency.ExitError == "target_connection_closed" {
			if _, ok := selected["exit-closed"]; !ok {
				selected["exit-closed"] = r.Config
			}
		}
	}
	runtime := NewMeasurements(measurement.NewExitLookup())
	defer func() { _ = runtime.Close(context.Background()) }()
	store := config.NewTLSPolicyStore(filepath.Join(directory, "tls-policies.json"), uint32(os.Getuid()))
	for _, label := range []string{"body-closed", "exit-closed"} {
		p, ok := selected[label]
		if !ok {
			continue
		}
		t.Run(label, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			p.TLSResolved, err = store.Resolve(ctx, p)
			if err != nil {
				t.Fatal("policy unavailable")
			}
			physical, err := network.PhysicalNetwork(ctx)
			if err != nil {
				t.Fatal("interface unavailable")
			}
			p = bindDiagnosticInterface(p, physical.Name)
			err = runtime.withMeasurementOutbound(ctx, p, func(ctx context.Context, out adapter.Outbound) error {
				for _, target := range []struct{ name, url string }{{"google204", "https://www.google.com/generate_204"}, {"ipwho", "https://ipwho.is/"}, {"cloudflare-trace", "https://www.cloudflare.com/cdn-cgi/trace"}, {"download", "https://speed.cloudflare.com/__down?bytes=8388608"}} {
					started := time.Now()
					var mu sync.Mutex
					dialOK, tlsStarted, tlsOK, headers := false, false, false, false
					var tlsType string
					transport := &http.Transport{Proxy: nil, DisableKeepAlives: os.Getenv("PAX_DIAGNOSTIC_KEEPALIVE") != "1", DialContext: func(ctx context.Context, kind, address string) (net.Conn, error) {
						c, e := out.DialContext(ctx, kind, M.ParseSocksaddr(address))
						mu.Lock()
						dialOK = e == nil
						mu.Unlock()
						if e != nil {
							return nil, nodeMeasurementFailure(e)
						}
						return c, nil
					}}
					task, stop := context.WithTimeout(ctx, 8*time.Second)
					trace := &httptrace.ClientTrace{TLSHandshakeStart: func() { mu.Lock(); tlsStarted = true; mu.Unlock() }, TLSHandshakeDone: func(_ tls.ConnectionState, e error) {
						mu.Lock()
						tlsOK = e == nil
						if e != nil {
							tlsType = fmt.Sprintf("%T", e)
						}
						mu.Unlock()
					}, GotFirstResponseByte: func() { mu.Lock(); headers = true; mu.Unlock() }}
					request, _ := http.NewRequestWithContext(httptrace.WithClientTrace(task, trace), "GET", target.url, nil)
					request.Header.Set("User-Agent", "PaxConnect/1.0")
					request.Header.Set("Accept-Encoding", "identity")
					client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
					response, e := client.Do(request)
					status := 0
					var received int64
					var length int64
					stage := "before_headers"
					if e == nil {
						status = response.StatusCode
						length = response.ContentLength
						stage = "body"
						budget := int64(16384)
						if target.name == "download" {
							budget = measurement.DownloadBudget
						}
						received, e = io.Copy(io.Discard, io.LimitReader(response.Body, budget))
						_ = response.Body.Close()
					}
					code, errorType := "", ""
					if e != nil {
						code = measurement.Classify(e, false).Code
						errorType = fmt.Sprintf("%T", e)
					}
					stop()
					transport.CloseIdleConnections()
					mu.Lock()
					t.Logf("target=%s keepalive=%t stage=%s status=%d node_dial=%t tls_started=%t tls_ok=%t tls_error_type=%s response_started=%t received=%d content_length=%d code=%s error_type=%s elapsed_ms=%d", target.name, os.Getenv("PAX_DIAGNOSTIC_KEEPALIVE") == "1", stage, status, dialOK, tlsStarted, tlsOK, tlsType, headers, received, length, code, errorType, time.Since(started).Milliseconds())
					mu.Unlock()
				}
				return nil
			})
			if err != nil {
				t.Log("runtime_prepare_failed")
			}
			if os.Getenv("PAX_DIAGNOSTIC_PRODUCTION") == "1" {
				start := time.Now()
				_ = runtime.withMeasurementOutbound(ctx, p, func(ctx context.Context, out adapter.Outbound) error {
					value, code, _ := measureExit(ctx, out, runtime.exits, "closure-diagnostic")
					t.Logf("production_exit success=%t code=%s elapsed_ms=%d", value != nil, code, time.Since(start).Milliseconds())
					return nil
				})
				value, e := runtime.DownloadRoute(ctx, p)
				code := ""
				if e != nil {
					code = measurement.Classify(e, false).Code
				}
				t.Logf("production_download success=%t code=%s bytes=%d setup_ms=%d body_ms=%d end=%s", e == nil, code, value.Bytes, value.Setup.Milliseconds(), value.Duration.Milliseconds(), value.End)
			}
		})
	}
}

func bindDiagnosticInterface(p config.RouteConfig, name string) config.RouteConfig {
	var doc map[string]any
	_ = json.Unmarshal(config.DocumentForRoute(p), &doc)
	doc["bind_interface"] = name
	p.Document, _ = json.Marshal(doc)
	return p
}
