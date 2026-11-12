//go:build integration && with_clash_api

package singbox

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/proxy"
)

func selectorNode(t *testing.T, label string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var connections sync.Map
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			connections.Store(c, true)
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer connections.Delete(c)
				defer func() { _ = c.Close() }()
				_ = c.SetDeadline(time.Now().Add(10 * time.Second))
				header := make([]byte, 2)
				if _, err := io.ReadFull(c, header); err != nil {
					return
				}
				methods := make([]byte, int(header[1]))
				if _, err := io.ReadFull(c, methods); err != nil {
					return
				}
				if _, err := c.Write([]byte{5, 0}); err != nil {
					return
				}
				request := make([]byte, 4)
				if _, err := io.ReadFull(c, request); err != nil {
					return
				}
				size := 0
				switch request[3] {
				case 1:
					size = 4
				case 4:
					size = 16
				case 3:
					var length [1]byte
					if _, err := io.ReadFull(c, length[:]); err != nil {
						return
					}
					size = int(length[0])
				default:
					return
				}
				if _, err := io.ReadFull(c, make([]byte, size+2)); err != nil {
					return
				}
				if _, err := c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil {
					return
				}
				reader := bufio.NewReader(c)
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					if _, err = c.Write([]byte(label + ":" + line)); err != nil {
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		connections.Range(func(key, value any) bool { _ = key.(net.Conn).Close(); return true })
		wg.Wait()
	})
	return listener.Addr().String()
}
func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return p
}
func socksFlow(t *testing.T, port int, target string) net.Conn {
	t.Helper()
	dialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", port), nil, &net.Dialer{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := dialer.Dial("tcp", target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}
func flowReply(t *testing.T, c net.Conn, want string) {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(time.Second))
	if _, err := io.WriteString(c, "ping\n"); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil || line != want+":ping\n" {
		t.Fatalf("got %q %v, want %s", line, err, want)
	}
}

func TestCoreSelectorSwitchClosesOnlyOldNodeFlows(t *testing.T) {
	a, b := selectorNode(t, "A"), selectorNode(t, "B")
	ap := mustNodePort(t, a)
	bp := mustNodePort(t, b)
	in := freeLoopbackPort(t)
	profile := fmt.Sprintf(`{"experimental":{"clash_api":{},"cache_file":{"path":%q}},"inbounds":[{"type":"mixed","tag":"mixed","listen":"127.0.0.1","listen_port":%d}],"outbounds":[{"type":"selector","tag":"proxy","outbounds":["A","B"],"default":"A"},{"type":"socks","tag":"A","server":"127.0.0.1","server_port":%s},{"type":"socks","tag":"B","server":"127.0.0.1","server_port":%s},{"type":"direct","tag":"direct"}],"route":{"final":"proxy","rules":[{"ip_cidr":["127.0.0.0/8"],"outbound":"direct"}]}}`, filepath.Join(t.TempDir(), "cache.db"), in, ap, bp)
	r := NewCoreRuntime(context.Background())
	defer r.Close()
	if err := r.Apply(profile); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Clear() }()
	old := socksFlow(t, in, "192.0.2.1:80")
	flowReply(t, old, "A")
	// Direct echo is unrelated to selector traffic and must survive the sweep.
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = echo.Close() }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := echo.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		reader := bufio.NewReader(c)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if _, err = io.WriteString(c, "direct:"+line); err != nil {
				return
			}
		}
	}()
	direct := socksFlow(t, in, echo.Addr().String())
	defer func() { _ = direct.Close(); <-done }()
	flowReply(t, direct, "direct")
	selected, closed, err := r.SelectRoute(context.Background(), "A", "B")
	if err != nil || selected != "B" || !closed {
		t.Fatalf("switch %q %v %v", selected, closed, err)
	}
	_ = old.SetReadDeadline(time.Now().Add(time.Second))
	var buf [1]byte
	if _, err = old.Read(buf[:]); err == nil {
		t.Fatal("old node flow survived")
	}
	next := socksFlow(t, in, "192.0.2.1:80")
	flowReply(t, next, "B")
	flowReply(t, direct, "direct")
}
func mustNodePort(t *testing.T, address string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestCorePhysicalChangeAndPauseWakeUseExistingInstance(t *testing.T) {
	node := selectorNode(t, "A")
	port := freeLoopbackPort(t)
	profile := fmt.Sprintf(`{"experimental":{"clash_api":{},"cache_file":{"path":%q}},"inbounds":[{"type":"mixed","listen":"127.0.0.1","listen_port":%d}],"outbounds":[{"type":"socks","tag":"proxy","server":"127.0.0.1","server_port":%s}],"route":{"final":"proxy"}}`, filepath.Join(t.TempDir(), "cache.db"), port, mustNodePort(t, node))
	r := NewCoreRuntime(context.Background())
	defer r.Close()
	if err := r.Apply(profile); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Clear() }()
	flow := socksFlow(t, port, "192.0.2.1:80")
	flowReply(t, flow, "A")
	r.DefaultInterfaceChanged("en0", 1)
	r.DefaultInterfaceChanged("utun9", 9)
	r.DefaultInterfaceChanged("en0", 1)
	flowReply(t, flow, "A")
	r.DefaultInterfaceChanged("en1", 2)
	_ = flow.SetReadDeadline(time.Now().Add(time.Second))
	var buf [1]byte
	if _, err := flow.Read(buf[:]); err == nil {
		t.Fatal("physical change kept old flow")
	}
	instance := r.current().Instance()
	if err := r.SetPaused(true); err != nil || !instance.PauseManager().IsDevicePaused() {
		t.Fatal("device not paused", err)
	}
	if err := r.SetPaused(false); err != nil || instance.PauseManager().IsDevicePaused() {
		t.Fatal("device not awake", err)
	}
	if instance != r.current().Instance() {
		t.Fatal("power rebuilt instance")
	}
	if err := r.Clear(); err != nil {
		t.Fatal(err)
	}
	if err := r.SetPaused(false); err != nil || r.Status() != "IDLE" {
		t.Fatal("wake revived idle runtime")
	}
}
