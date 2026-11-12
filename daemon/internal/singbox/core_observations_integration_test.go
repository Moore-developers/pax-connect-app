//go:build integration

package singbox

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/sagernet/sing-box/daemon"
	"github.com/sagernet/sing-box/experimental/paxobs"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeConnectionHistoryUsesOneIdentityAndRealCounters(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, strings.Repeat("x", 4096)) }))
	defer target.Close()
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := reservation.Addr().(*net.TCPAddr).Port
	_ = reservation.Close()
	r := NewCoreRuntime(context.Background())
	defer r.Close()
	profile := fmt.Sprintf(`{"log":{"level":"info"},"experimental":{"cache_file":{"path":%q}},"inbounds":[{"type":"mixed","tag":"test","listen":"127.0.0.1","listen_port":%d}],"outbounds":[{"type":"direct","tag":"direct"}]}`, filepath.Join(t.TempDir(), "cache.db"), port)
	if err = r.Apply(profile); err != nil {
		t.Fatal(err)
	}
	proxy, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	res, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Clear(); err != nil {
		t.Fatal(err)
	}
	b := r.ReadObservations(context.Background(), "", 0, 256)
	ids := map[string]bool{}
	var downloaded uint64
	var started, closed bool
	for _, e := range b.Records {
		if e.Flow == nil {
			continue
		}
		ids[e.Flow.Key] = true
		downloaded += e.Download
		started = started || e.Kind == "started"
		closed = closed || e.Kind == "closed"
		if e.Flow.Direction != "direct" {
			t.Fatalf("wrong actual direction: %v", e.Flow)
		}
	}
	if len(ids) != 1 || !started || !closed || downloaded < 4096 {
		t.Fatalf("facts: ids=%d started=%v closed=%v download=%d batch=%+v", len(ids), started, closed, downloaded, b)
	}
	replay := r.ReadObservations(context.Background(), b.Session, 0, 256)
	if len(replay.Records) != len(b.Records) {
		t.Fatal("read destructively consumed records")
	}
}

func TestObservationPollingDoesNotRaceCoreLifecycle(t *testing.T) {
	r := NewCoreRuntime(context.Background())
	defer r.Close()
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for {
			select {
			case <-done:
				return
			default:
				r.ReadObservations(context.Background(), "", 0, 256)
				r.ReadUsage(context.Background())
			}
		}
	}()
	for i := 0; i < 3; i++ {
		profile := fmt.Sprintf(`{"log":{"level":"error"},"experimental":{"cache_file":{"path":%q}},"outbounds":[{"type":"direct","tag":"direct"}]}`, filepath.Join(t.TempDir(), "cache.db"))
		if err := r.Apply(profile); err != nil {
			close(done)
			<-finished
			t.Fatal(err)
		}
		if err := r.Clear(); err != nil {
			close(done)
			<-finished
			t.Fatal(err)
		}
	}
	close(done)
	<-finished
}

func TestObservationBandwidthRegression(t *testing.T) {
	if os.Getenv("PAX_HISTORY_BANDWIDTH") != "1" {
		t.Skip("explicit three-round bandwidth comparison")
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const chunks = 128
		data := make([]byte, 16<<10)
		start := time.Now()
		for i := 0; i < chunks; i++ {
			if _, err := w.Write(data); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(time.Until(start.Add(time.Duration(i+1) * time.Second / 64)))
		}
	}))
	defer target.Close()
	run := func(observe bool) time.Duration {
		t.Helper()
		reserved, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := reserved.Addr().(*net.TCPAddr).Port
		_ = reserved.Close()
		r := NewCoreRuntime(context.Background())
		if !observe {
			r.current().Close()
			r.ctx = paxobs.WithSink(r.ctx, nil)
			r.svc = daemon.NewStartedService(daemon.ServiceOptions{Context: r.ctx, Handler: r.handler, Debug: true, LogMaxLines: 256})
		}
		defer r.Close()
		profile := fmt.Sprintf(`{"log":{"level":"error"},"experimental":{"cache_file":{"path":%q}},"inbounds":[{"type":"mixed","listen":"127.0.0.1","listen_port":%d}],"outbounds":[{"type":"direct","tag":"direct"}]}`, filepath.Join(t.TempDir(), "cache.db"), port)
		if err = r.Apply(profile); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = r.Clear() }()
		proxy, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
		transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
		start := time.Now()
		res, err := client.Get(target.URL)
		if err != nil {
			t.Fatal(err)
		}
		n, err := io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		if err != nil || n != 2<<20 {
			t.Fatal(n, err)
		}
		return time.Since(start)
	}
	var baseline, observed time.Duration
	for round := 0; round < 3; round++ {
		a, b := run(false), run(true)
		baseline += a
		observed += b
		t.Logf("round%d baseline=%s observed=%s", round+1, a, b)
	}
	regression := float64(observed)/float64(baseline) - 1
	t.Logf("paced 1MiB/s three-round transfer-duration regression %.2f%%", regression*100)
	if regression > 0.05 {
		t.Error("capture exceeded5% throughput regression", regression)
	}
}

func TestProxyHistoryHasObservedResponseTiming(t *testing.T) {
	address, _, _ := forwardingNode(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer target.Close()
	host, p, _ := net.SplitHostPort(address)
	port := freeLoopbackPort(t)
	r := NewCoreRuntime(context.Background())
	defer r.Close()
	profile := fmt.Sprintf(`{"log":{"level":"error"},"experimental":{"cache_file":{"path":%q}},"inbounds":[{"type":"mixed","tag":"mixed","listen":"127.0.0.1","listen_port":%d}],"outbounds":[{"type":"socks","tag":"proxy","server":%q,"server_port":%s}],"route":{"final":"proxy"}}`, filepath.Join(t.TempDir(), "cache.db"), port, host, p)
	r.SetRoutingSources([]string{`{"Package":"测试包","Method":"domain_suffix","Target":"example.com"}`})
	// Include a concrete matching predicate in the existing direct loopback SOCKS path.
	var document map[string]any
	if err := json.Unmarshal([]byte(profile), &document); err != nil {
		t.Fatal(err)
	}
	document["route"].(map[string]any)["rules"] = []any{map[string]any{"ip_cidr": []string{"127.0.0.1/32"}, "outbound": "proxy"}}
	canonical, _ := json.Marshal(document)
	profile = string(canonical)
	if err := r.Apply(profile); err != nil {
		t.Fatal(err)
	}
	proxy, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	res, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	_ = r.Clear()
	batch := r.ReadObservations(context.Background(), "", 0, 256)
	for _, event := range batch.Records {
		if event.Kind == "response" && event.Flow != nil && event.Flow.ResponseKnown && event.Flow.RuleSource == "Pax 包：测试包" && event.Flow.Rule == "domain_suffix=example.com" {
			return
		}
	}
	t.Fatal("successful proxy response has no observed timing")
}
