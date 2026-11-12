//go:build integration && with_clash_api

package singbox

import (
	"bufio"
	"context"
	"encoding/json"
	"golang.org/x/net/proxy"
	"io"
	"net"
	"path/filepath"
	"pax-connect/daemon/internal/config"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func forwardingNode(t *testing.T) (string, *atomic.Int32, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	count := new(atomic.Int32)
	var conns sync.Map
	var wg sync.WaitGroup
	stop := func() {
		_ = listener.Close()
		conns.Range(func(k, v any) bool { _ = k.(net.Conn).Close(); return true })
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			count.Add(1)
			conns.Store(conn, true)
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conns.Delete(conn)
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				head := make([]byte, 2)
				if _, err := io.ReadFull(conn, head); err != nil {
					return
				}
				if _, err := io.CopyN(io.Discard, conn, int64(head[1])); err != nil {
					return
				}
				if _, err := conn.Write([]byte{5, 0}); err != nil {
					return
				}
				req := make([]byte, 4)
				if _, err := io.ReadFull(conn, req); err != nil {
					return
				}
				host := ""
				switch req[3] {
				case 1:
					b := make([]byte, 4)
					if _, err := io.ReadFull(conn, b); err != nil {
						return
					}
					host = net.IP(b).String()
				case 3:
					var n [1]byte
					if _, err := io.ReadFull(conn, n[:]); err != nil {
						return
					}
					b := make([]byte, n[0])
					if _, err := io.ReadFull(conn, b); err != nil {
						return
					}
					host = string(b)
				default:
					return
				}
				var ports [2]byte
				if _, err := io.ReadFull(conn, ports[:]); err != nil {
					return
				}
				target, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(int(ports[0])<<8|int(ports[1]))), time.Second)
				if err != nil {
					return
				}
				defer func() { _ = target.Close() }()
				if _, err := conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil {
					return
				}
				done := make(chan struct{})
				go func() { _, _ = io.Copy(target, conn); _ = target.Close(); close(done) }()
				_, _ = io.Copy(conn, target)
				_ = conn.Close()
				<-done
			}()
		}
	}()
	t.Cleanup(func() { stop(); wg.Wait() })
	return listener.Addr().String(), count, stop
}
func TestNativeChainUsesBothHopsAndNeverFallsBack(t *testing.T) {
	front, frontCount, stopFront := forwardingNode(t)
	last, lastCount, _ := forwardingNode(t)
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = echo.Close() }()
	go func() {
		conn, err := echo.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = io.Copy(conn, conn)
	}()
	makeProfile := func(address string) config.RouteConfig {
		h, p, _ := net.SplitHostPort(address)
		n, _ := strconv.Atoi(p)
		raw, _ := json.Marshal(map[string]any{"type": "socks", "server": h, "server_port": n})
		return config.RouteConfig{NodeProfile: config.NodeProfile{ID: address, ProtocolName: "socks", Server: h, Port: uint16(n)}, Document: raw}
	}
	a, b := makeProfile(front), makeProfile(last)
	b.ID = "pair"
	b.Front = &a
	port := freeLoopbackPort(t)
	production, err := CoreCatalogProfile(context.Background(), []config.RouteConfig{a, b}, b, testRules(t), net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ValidateCoreOptions(context.Background(), []byte(production), t.TempDir(), port); err != nil {
		t.Fatal("root profile admission rejected chain", err)
	}
	var doc map[string]any
	if err = json.Unmarshal([]byte(production), &doc); err != nil {
		t.Fatal(err)
	}
	tags := map[string]bool{}
	for _, item := range doc["outbounds"].([]any) {
		tags[item.(map[string]any)["tag"].(string)] = true
	}
	for _, item := range doc["dns"].(map[string]any)["servers"].([]any) {
		if detour, _ := item.(map[string]any)["detour"].(string); detour != "" && !tags[detour] {
			t.Fatalf("dangling DNS detour %s", detour)
		}
	}
	// Exercise the production selector/DNS/outbounds with only privileged I/O replaced.
	doc["experimental"] = map[string]any{"clash_api": map[string]any{}, "cache_file": map[string]any{"path": filepath.Join(t.TempDir(), "cache.db")}}
	doc["inbounds"] = []any{map[string]any{"type": "mixed", "tag": "mixed", "listen": "127.0.0.1", "listen_port": port}}
	doc["route"] = map[string]any{"final": "proxy"}
	delete(doc["dns"].(map[string]any), "rules")
	raw, _ := json.Marshal(doc)
	runtime := NewCoreRuntime(context.Background())
	defer runtime.Close()
	if err = runtime.Apply(string(raw)); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runtime.Clear() }()
	conn := socksFlow(t, port, echo.Addr().String())
	if _, err = io.WriteString(conn, "native-chain\n"); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || line != "native-chain\n" {
		t.Fatal(line, err)
	}
	if frontCount.Load() != 1 || lastCount.Load() != 1 {
		t.Fatal("did not use exactly both hops", frontCount.Load(), lastCount.Load())
	}
	usage := runtime.ReadUsage(context.Background())
	if len(usage.Counters) == 0 {
		t.Fatal("native user traffic counters missing")
	}
	stopFront()
	dialer, _ := proxy.SOCKS5("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), nil, &net.Dialer{Timeout: time.Second})
	failed, err := dialer.Dial("tcp", echo.Addr().String())
	if err == nil {
		_ = failed.Close()
		t.Fatal("front failure bypassed to final")
	}
}
