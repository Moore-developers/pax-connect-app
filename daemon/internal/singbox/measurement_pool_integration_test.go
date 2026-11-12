//go:build integration && with_clash_api

package singbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
	"strconv"
	"sync"
	"testing"
	"time"
)

func measurementSOCKS(address, id string) config.RouteConfig {
	host, port, _ := net.SplitHostPort(address)
	n, _ := strconv.Atoi(port)
	raw, _ := json.Marshal(map[string]any{"type": "socks", "server": host, "server_port": n})
	return config.RouteConfig{NodeProfile: config.NodeProfile{ID: id, Name: id, ProtocolName: "socks", Server: host, Port: uint16(n)}, Document: raw, Security: "none", Transport: "tcp"}
}
func TestSharedMeasurementInstanceRoutesConcurrentJobsAndKeepsFreshConnections(t *testing.T) {
	a, countA, _ := forwardingNode(t)
	b, countB, _ := forwardingNode(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "fixture") }))
	defer target.Close()
	runtime := NewMeasurements(measurement.NewExitLookup())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := runtime.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	run := func(ctx context.Context, p config.RouteConfig) error {
		return runtime.WithRouteNetwork(ctx, p, func(dial func(context.Context, string, string) (net.Conn, error), _ func(context.Context, string) ([]netip.Addr, error)) error {
			transport := &http.Transport{DialContext: dial, Proxy: nil}
			defer transport.CloseIdleConnections()
			request, _ := http.NewRequestWithContext(ctx, "GET", target.URL, nil)
			response, err := (&http.Client{Transport: transport}).Do(request)
			if err != nil {
				return err
			}
			defer response.Body.Close()
			value, err := io.ReadAll(response.Body)
			if string(value) != "fixture" {
				return fmt.Errorf("wrong route response")
			}
			return err
		})
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, p := range []config.RouteConfig{measurementSOCKS(a, "same-id"), measurementSOCKS(b, "same-id")} {
		wg.Add(1)
		go func(p config.RouteConfig) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			results <- run(ctx, p)
		}(p)
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if countA.Load() != 1 || countB.Load() != 1 {
		t.Fatal("parallel jobs crossed frozen routes", countA.Load(), countB.Load())
	}
	shared := runtime.instance
	if shared == nil {
		t.Fatal("no measurement instance")
	}
	if err := run(context.Background(), measurementSOCKS(a, "same-id")); err != nil {
		t.Fatal(err)
	}
	if runtime.instance != shared || countA.Load() != 2 {
		t.Fatal("instance recreated or old task connection reused")
	}
}

func TestSharedMeasurementCancellationDoesNotStopAnotherJobOrBox(t *testing.T) {
	node, _, _ := forwardingNode(t)
	entered := make(chan struct{}, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wait" {
			entered <- struct{}{}
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()
	runtime := NewMeasurements(measurement.NewExitLookup())
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := runtime.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	p := measurementSOCKS(node, "r")
	run := func(ctx context.Context, path string) error {
		return runtime.WithRouteNetwork(ctx, p, func(dial func(context.Context, string, string) (net.Conn, error), _ func(context.Context, string) ([]netip.Addr, error)) error {
			transport := &http.Transport{DialContext: dial, Proxy: nil}
			defer transport.CloseIdleConnections()
			req, _ := http.NewRequestWithContext(ctx, "GET", target.URL+path, nil)
			resp, err := (&http.Client{Transport: transport}).Do(req)
			if err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
			return err
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, "/wait") }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request not started")
	}
	cancel()
	if err := run(context.Background(), "/ok"); err != nil {
		t.Fatal("unrelated job interrupted", err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancel became success")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("task did not stop")
	}
	shared := runtime.instance
	ctx2, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := runtime.Close(ctx2); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), "/ok"); err == nil {
		t.Fatal("closed runtime revived")
	}
	if runtime.instance != shared {
		t.Fatal("box replaced")
	}
}
func TestSharedMeasurementChainTraversesBothNativeHops(t *testing.T) {
	a, countA, stopA := forwardingNode(t)
	b, countB, _ := forwardingNode(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer target.Close()
	runtime := NewMeasurements(measurement.NewExitLookup())
	defer func() { _ = runtime.Close(context.Background()) }()
	front, last := measurementSOCKS(a, "front"), measurementSOCKS(b, "final")
	last.Front = &front
	run := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		return runtime.WithRouteNetwork(ctx, last, func(dial func(context.Context, string, string) (net.Conn, error), _ func(context.Context, string) ([]netip.Addr, error)) error {
			transport := &http.Transport{DialContext: dial, Proxy: nil}
			defer transport.CloseIdleConnections()
			req, _ := http.NewRequestWithContext(ctx, "GET", target.URL, nil)
			resp, err := (&http.Client{Transport: transport}).Do(req)
			if err == nil {
				_ = resp.Body.Close()
			}
			return err
		})
	}
	if err := run(); err != nil {
		t.Fatal(err)
	}
	if countA.Load() != 1 || countB.Load() != 1 {
		t.Fatal("chain bypass", countA.Load(), countB.Load())
	}
	stopA()
	if err := run(); err == nil {
		t.Fatal("broken front bypassed")
	}
	if countB.Load() != 1 {
		t.Fatal("final reached without front")
	}
}

func TestLatencyReportsBeforeExitCancellation(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer target.Close()
	previous := latencyTargets
	latencyTargets = []string{target.URL}
	defer func() { latencyTargets = previous }()
	address, _, _ := forwardingNode(t)
	runtime := NewMeasurements(measurement.NewExitLookup())
	defer func() { _ = runtime.Close(context.Background()) }()
	ctx, cancel := context.WithTimeout(context.Background(), measurement.TotalMeasureTimeout)
	defer cancel()
	observed := false

	value, err := runtime.MeasureRoute(ctx, measurementSOCKS(address, "route"), func(value config.RouteMeasurement) { observed = value.Cold > 0; cancel() })
	if err == nil || !observed || value.Cold <= 0 {
		t.Fatal(value, err, observed)
	}
}
