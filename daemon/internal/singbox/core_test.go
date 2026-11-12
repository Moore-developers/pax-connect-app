package singbox

import (
	"context"
	"fmt"
	"github.com/sagernet/sing-box/daemon"
	"google.golang.org/grpc"
	"net"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoreRuntimeRetiresFatalService(t *testing.T) {
	r := NewCoreRuntime(context.Background())
	defer r.Close()
	// A reserved local listener causes a real start failure after box.New.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	port := l.Addr().(*net.TCPAddr).Port
	cache := filepath.Join(t.TempDir(), "cache.db")
	profile := fmt.Sprintf(`{"log":{"level":"panic"},"experimental":{"cache_file":{"path":%q}},"inbounds":[{"type":"mixed","listen":"127.0.0.1","listen_port":%d}],"outbounds":[{"type":"direct","tag":"direct"}]}`, cache, port)
	if err = r.Apply(profile); err == nil {
		t.Fatal("occupied local port accepted")
	}
	if r.Status() != "FATAL" {
		t.Fatal(r.Status())
	}
	old := r.current()
	if err = r.Clear(); err != nil {
		t.Fatal(err)
	}
	if r.Status() != "IDLE" || r.current().Instance() != nil || old == r.current() {
		t.Fatal("fatal instance not retired")
	}
	profile = fmt.Sprintf(`{"log":{"level":"panic"},"experimental":{"cache_file":{"path":%q}},"outbounds":[{"type":"direct","tag":"direct"}]}`, cache)
	if err = r.Apply(profile); err != nil {
		t.Fatal(err)
	}
	if r.Status() != "STARTED" {
		t.Fatal(r.Status())
	}
	if err = r.Clear(); err != nil {
		t.Fatal(err)
	}
}
func TestCorePlatformBoundedAndUnsupported(t *testing.T) {
	h := &corePlatform{}
	if h.ServiceStop() == nil || h.ServiceReload() == nil || h.SetSystemProxyEnabled(true) == nil || h.TriggerNativeCrash() == nil {
		t.Fatal("platform operation accepted")
	}
	if _, err := h.SystemProxyStatus(); err == nil {
		t.Fatal("proxy status accepted")
	}
	if _, err := h.ConnectSSHAgent(); err == nil {
		t.Fatal("SSH accepted")
	}
	for i := 0; i < 512; i++ {
		h.WriteDebugMessage(strings.Repeat("x", 5000))
	}
	for _, line := range h.lines {
		if len(line) != 4096 {
			t.Fatalf("line bytes %d", len(line))
		}
	}
	if h.next != 0 {
		t.Fatal("ring did not wrap")
	}
}
func TestCoreAllowlistMatchesUpstreamDescriptor(t *testing.T) {
	// Descriptor presence guards the separate core interceptor's service prefix.
	if daemon.StartedService_ServiceDesc.ServiceName != "daemon.StartedService" {
		t.Fatal("upstream service renamed")
	}
}

// compile-time proof the registrar boundary remains the gRPC interface.
var _ grpc.ServiceRegistrar = coreRegistrar{}

func TestCoreFailureInServiceRing(t *testing.T) {
	r := NewCoreRuntime(context.Background())
	defer r.Close()
	r.RecordFailure("core_profile_start_failed: address_in_use")
	stream := &coreLogSnapshot{}
	_ = r.current().SubscribeLog(nil, stream)
	if len(stream.messages) != 1 || stream.messages[0] != "core_profile_start_failed: address_in_use" {
		t.Fatal("failure missing from service ring")
	}
	if r.handler.lines[0] != stream.messages[0] {
		t.Fatal("failure missing from platform ring")
	}
}

type coreLogSnapshot struct {
	grpc.ServerStream
	messages []string
}

func (*coreLogSnapshot) Context() context.Context { return context.Background() }
func (s *coreLogSnapshot) Send(log *daemon.Log) error {
	for _, m := range log.Messages {
		s.messages = append(s.messages, m.Message)
	}
	return fmt.Errorf("snapshot done")
}
