//go:build integration

package singbox

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"pax-connect/daemon/internal/config"
	"strconv"
	"testing"
	"time"
)

func TestCurrentMixedPathProvesActualExitAndExcludesProbe(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer target.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	r := NewCoreRuntime(context.Background())
	defer r.Close()
	profile := fmt.Sprintf(`{"log":{"level":"error"},"experimental":{"cache_file":{"path":%q}},"inbounds":[{"type":"mixed","tag":"mixed-in","listen":"127.0.0.1","listen_port":%d}],"outbounds":[{"type":"selector","tag":"proxy","outbounds":["pax-node-fixture"],"default":"pax-node-fixture"},{"type":"direct","tag":"pax-node-fixture"}],"route":{"final":"proxy"}}`, filepath.Join(t.TempDir(), "cache.db"), port)
	if err = r.Apply(profile); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Clear() }()
	r.pathTarget = target.URL
	r.pathTLS = &tls.Config{RootCAs: target.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs}
	q := config.CurrentPathRequest{CheckID: "test", ExpectedTag: "pax-node-fixture"}
	run, err := r.PrepareCurrentPath(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	result := run(context.Background())
	if result.State != "passed" || result.ActualTag != "pax-node-fixture" {
		t.Fatalf("current path proof: %+v", result)
	}
	batch := r.ReadObservations(context.Background(), "", 0, 256)
	if len(batch.Records) != 0 {
		t.Fatalf("probe polluted user records: %+v", batch)
	}
}

func TestCurrentMixedPathChecksBothFrozenHops(t *testing.T) {
	front, frontCount, _ := forwardingNode(t)
	last, lastCount, _ := forwardingNode(t)
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer target.Close()
	node := func(address, tag, detour string) map[string]any {
		host, port, _ := net.SplitHostPort(address)
		n, _ := strconv.Atoi(port)
		v := map[string]any{"type": "socks", "tag": tag, "server": host, "server_port": n}
		if detour != "" {
			v["detour"] = detour
		}
		return v
	}
	port := freeLoopbackPort(t)
	doc := map[string]any{"log": map[string]any{"level": "error"}, "experimental": map[string]any{"cache_file": map[string]any{"path": filepath.Join(t.TempDir(), "cache.db")}}, "inbounds": []any{map[string]any{"type": "mixed", "tag": "mixed", "listen": "127.0.0.1", "listen_port": port}}, "outbounds": []any{map[string]any{"type": "selector", "tag": "proxy", "outbounds": []string{"pair"}, "default": "pair"}, node(front, "front", ""), node(last, "pair", "front")}, "route": map[string]any{"final": "proxy"}}
	raw, _ := json.Marshal(doc)
	r := NewCoreRuntime(context.Background())
	defer r.Close()
	if err := r.Apply(string(raw)); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Clear() }()
	r.pathTarget = target.URL
	r.pathTLS = &tls.Config{RootCAs: target.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs}
	run, err := r.PrepareCurrentPath(context.Background(), config.CurrentPathRequest{CheckID: "both", ExpectedTag: "pair"})
	if err != nil {
		t.Fatal(err)
	}
	result := run(context.Background())
	if result.State != "passed" || frontCount.Load() != 1 || lastCount.Load() != 1 {
		t.Fatalf("two-hop proof: %+v front=%d last=%d", result, frontCount.Load(), lastCount.Load())
	}
	if records := r.ReadObservations(context.Background(), "", 0, 256).Records; len(records) != 0 {
		t.Fatalf("probe leaked: %v", records)
	}
}

func TestCurrentMixedPathTargetFailuresRemainInconclusive(t *testing.T) {
	for _, mode := range []string{"redirect", "certificate", "timeout", "wrong-exit"} {
		t.Run(mode, func(t *testing.T) {
			target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				if mode == "timeout" {
					<-q.Context().Done()
					return
				}
				if mode == "redirect" {
					w.Header().Set("Location", "https://must-not-follow.invalid")
					w.WriteHeader(302)
					return
				}
				w.WriteHeader(204)
			}))
			defer target.Close()
			port := freeLoopbackPort(t)
			r := NewCoreRuntime(context.Background())
			defer r.Close()
			final := "proxy"
			if mode == "wrong-exit" {
				final = "other"
			}
			profile := fmt.Sprintf(`{"log":{"level":"fatal"},"experimental":{"cache_file":{"path":%q}},"inbounds":[{"type":"mixed","tag":"mixed","listen":"127.0.0.1","listen_port":%d}],"outbounds":[{"type":"selector","tag":"proxy","outbounds":["node"],"default":"node"},{"type":"direct","tag":"node"},{"type":"direct","tag":"other"}],"route":{"final":%q}}`, filepath.Join(t.TempDir(), "cache.db"), port, final)
			if err := r.Apply(profile); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = r.Clear() }()
			r.pathTarget = target.URL
			if mode != "certificate" {
				r.pathTLS = &tls.Config{RootCAs: target.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			run, err := r.PrepareCurrentPath(ctx, config.CurrentPathRequest{CheckID: mode, ExpectedTag: "node"})
			if err != nil {
				t.Fatal(err)
			}
			result := run(ctx)
			expected := "inconclusive"
			if mode == "wrong-exit" {
				expected = "failed"
			}
			if result.State != expected {
				t.Fatalf("%s: %+v", mode, result)
			}
			if records := r.ReadObservations(context.Background(), "", 0, 256).Records; len(records) != 0 {
				t.Fatalf("probe leaked: %v", records)
			}
		})
	}
}
