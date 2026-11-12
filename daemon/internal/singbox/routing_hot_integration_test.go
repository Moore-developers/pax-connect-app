//go:build integration

package singbox

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Loopback only: no TUN, root, public network, system proxy or user configuration.
func TestRoutingHotUpdateKeepsExistingTCPAndRejectsNewMatch(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = echo.Close() }()
	echoed := make(chan struct{})
	go func() {
		defer close(echoed)
		conn, err := echo.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = io.Copy(conn, conn)
	}()
	portLease, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := portLease.Addr().(*net.TCPAddr).Port
	_ = portLease.Close()
	r := NewCoreRuntime(context.Background())
	defer r.Close()
	doc := map[string]any{
		"log":          map[string]any{"disabled": true},
		"experimental": map[string]any{"cache_file": map[string]any{"enabled": true, "path": filepath.Join(t.TempDir(), "cache.db")}},
		"outbounds":    []any{map[string]any{"type": "direct", "tag": "direct"}},
		"inbounds":     []any{map[string]any{"type": "mixed", "tag": "test-mixed", "listen": "127.0.0.1", "listen_port": port}},
		"route":        map[string]any{"final": "direct", "find_process": true, "rules": []any{}},
	}
	raw, _ := json.Marshal(doc)
	if err := r.Apply(string(raw)); err != nil {
		t.Fatal(err)
	}
	tunnel := func() (net.Conn, error) {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
		if err != nil {
			return nil, err
		}
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", echo.Addr(), echo.Addr()); err != nil {
			_ = conn.Close()
			return nil, err
		}
		response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "CONNECT"})
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		if response.StatusCode != http.StatusOK {
			_ = conn.Close()
			return nil, fmt.Errorf("tunnel rejected: %d", response.StatusCode)
		}
		return conn, nil
	}
	conn, err := tunnel()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	exchange := func(text string) {
		t.Helper()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := io.WriteString(conn, text); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(text))
		if _, err := io.ReadFull(conn, got); err != nil || string(got) != text {
			t.Fatal("existing stream interrupted", string(got), err)
		}
	}
	exchange("before")
	instance := r.current().Instance()
	doc["route"].(map[string]any)["rules"] = []any{map[string]any{"ip_cidr": []string{"127.0.0.1/32"}, "action": "reject"}}
	raw, _ = json.Marshal(doc)
	if err := r.UpdateRouting(context.Background(), string(raw)); err != nil {
		t.Fatal(err)
	}
	if r.current().Instance() != instance {
		t.Fatal("instance restarted")
	}
	exchange("after")
	if blocked, err := tunnel(); err == nil {
		// mixed may acknowledge CONNECT before routing. Actual echoed payload, rather
		// than HTTP 200, is the evidence of a working destination connection.
		_ = blocked.SetDeadline(time.Now().Add(2 * time.Second))
		_, writeErr := io.WriteString(blocked, "blocked")
		got := make([]byte, 7)
		_, readErr := io.ReadFull(blocked, got)
		_ = blocked.Close()
		if writeErr == nil && readErr == nil && string(got) == "blocked" {
			t.Fatal("new connection bypassed updated reject rule")
		}
	}
	_ = conn.Close()
	select {
	case <-echoed:
	case <-time.After(time.Second):
		t.Fatal("echo worker did not drain")
	}
}
