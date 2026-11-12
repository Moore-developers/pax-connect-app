//go:build protocolintegration

package singbox

import (
	"context"
	"encoding/json"
	"fmt"
	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/option"
	"net"
	"net/http"
	"net/http/httptest"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
	"sync"
	"testing"
	"time"
)

// BenchmarkProtocolCapacity includes a fresh real sing-box SS instance per
// measurement, encrypted loopback forwarding and a 50ms HTTP target response.
// It is opt-in, never runs against public nodes and never creates a TUN.
func BenchmarkProtocolCapacity(b *testing.B) {
	// The product verifies target TLS. Use HTTP here to avoid installing a test
	// CA globally; the independent HTTPS benchmark covers target TLS overhead.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	data, _ := json.Marshal(map[string]any{
		"log":       map[string]any{"disabled": true},
		"inbounds":  []any{map[string]any{"type": "shadowsocks", "listen": "127.0.0.1", "listen_port": port, "method": "aes-128-gcm", "password": "fixture-password", "network": "tcp"}},
		"outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}}, "route": map[string]any{"final": "direct"},
	})
	ctx, cancel := context.WithCancel(runtimeContext(context.Background()))
	defer cancel()
	var options option.Options
	if err := options.UnmarshalJSONContext(ctx, data); err != nil {
		b.Fatal(err)
	}
	server, err := box.New(box.Options{Context: ctx, Options: options})
	if err != nil {
		b.Fatal(err)
	}
	if err := server.Start(); err != nil {
		b.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	previous := latencyTargets
	latencyTargets = []string{target.URL}
	defer func() { latencyTargets = previous }()
	runtime := NewMeasurements(measurement.NewExitLookup())
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = runtime.Close(ctx)
	}()
	profile := config.RouteConfig{NodeProfile: config.NodeProfile{ID: "benchmark", Name: "benchmark", ProtocolName: "shadowsocks", Server: "127.0.0.1", Port: uint16(port)}, SchemaVersion: 1, Method: "aes-128-gcm", Password: "fixture-password", Transport: "tcp", Security: "none"}
	for _, parallel := range []int{20, 32, 48, 64, 100} {
		b.Run(fmt.Sprint(parallel), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				jobs := make(chan struct{}, 200)
				for i := 0; i < 200; i++ {
					jobs <- struct{}{}
				}
				close(jobs)
				var done sync.WaitGroup
				for i := 0; i < parallel; i++ {
					done.Add(1)
					go func() {
						defer done.Done()
						for range jobs {
							measure, stop := context.WithTimeout(context.Background(), 10*time.Second)
							_, err := runtime.TestRoute(measure, profile)
							stop()
							if err != nil {
								b.Error(err)
							}
						}
					}()
				}
				done.Wait()
			}
		})
	}
}
