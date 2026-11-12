//go:build integration

package core

import (
	"context"
	"fmt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"net"
	"os"
	"path/filepath"
	pb "pax-connect/daemon/gen/proto/corev1"
	"pax-connect/daemon/internal/processidentity"
	"pax-connect/daemon/internal/singbox"
	"testing"
	"time"
)

func TestInProcessStartedService(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	o := Options{OwnerPID: os.Getpid(), OwnerUID: os.Getuid(), MixedPort: 17893, Socket: filepath.Join(t.TempDir(), "core.sock"), State: t.TempDir(), Resources: t.TempDir(), Version: "test"}
	r := singbox.NewCoreRuntime(context.Background())
	ready := make(chan struct{})
	h := nativeHost()
	h.lease = nil
	h.conflict = nil
	h.release = nil
	h.euid = func() int { return 0 }
	h.prepare = func(Options) error { return nil }
	h.copyRules = func(Options) error { return nil }
	h.cleanup = func(context.Context, int) error { return nil }
	h.listen = func(o Options) (net.Listener, error) {
		l, err := bindSocket(o, func(string) error { return nil })
		if err == nil {
			close(ready)
		}
		return l, err
	}
	result := make(chan error, 1)
	go func() { result <- run(ctx, o, r, singbox.ValidateCoreOptions, h) }()
	select {
	case <-ready:
	case err := <-result:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	conn, err := grpc.NewClient("unix://"+o.Socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	c := pb.NewPaxCoreClient(conn)
	empty := &emptypb.Empty{}
	// Port zero asks the kernel for an ephemeral loopback port. No TUN, DNS,
	// system proxy, probes or remote requests are part of this fixture.
	profile := `{"inbounds":[{"type":"mixed","listen":"127.0.0.1","listen_port":0}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`
	for cycle := 1; cycle <= 2; cycle++ {
		ack, err := c.ApplyProfile(ctx, &pb.ApplyProfileRequest{ProfileJson: profile, Revision: uint64(cycle)})
		if err != nil || ack.Revision != uint64(cycle) {
			t.Fatalf("apply %d: %v %v", cycle, ack, err)
		}
		hello, err := c.Hello(ctx, empty)
		if err != nil || hello.ServiceStatus != "STARTED" {
			t.Fatalf("hello %v %v", hello, err)
		}
	}
	if _, err = c.ClearProfile(ctx, empty); err != nil {
		t.Fatal(err)
	}
	hello, err := c.Hello(ctx, empty)
	if err != nil || hello.ServiceStatus != "IDLE" || r.Status() != "IDLE" {
		t.Fatalf("clear %v %v", hello, err)
	}
	// Exercise the installed interceptor through the wire, for unary and stream.
	if err = conn.Invoke(ctx, "/daemon.StartedService/TailscaleLogout", empty, empty); status.Code(err) != codes.PermissionDenied {
		t.Fatal(err)
	}
	stream, err := conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true}, "/daemon.StartedService/SubscribeTailscaleStatus")
	if err != nil {
		t.Fatal(err)
	}
	if err = stream.SendMsg(empty); err != nil {
		t.Fatal(err)
	}
	if err = stream.CloseSend(); err != nil {
		t.Fatal(err)
	}
	if err = stream.RecvMsg(empty); status.Code(err) != codes.PermissionDenied {
		t.Fatal(err)
	}
	if _, err = c.Shutdown(ctx, empty); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err = os.Lstat(o.Socket); !os.IsNotExist(err) {
		t.Fatalf("socket remains: %v", err)
	}
}
func TestRunOwnerDeathCleanup(t *testing.T) {
	for _, failure := range []string{"none", "transient", "persistent"} {
		t.Run(failure, func(t *testing.T) { testOwnerDeath(t, failure) })
	}
}
func testOwnerDeath(t *testing.T, failure string) {
	r := &fakeRuntime{state: "STARTED", failClear: failure == "persistent", clearFailures: map[string]int{"transient": 1}[failure]}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ticks := make(chan time.Time, 1)
	ticks <- time.Now()
	dir, dirErr := os.MkdirTemp("/tmp", "pc-fix-")
	if dirErr != nil {
		t.Fatal(dirErr)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	o := Options{OwnerPID: 123, OwnerUID: 501, MixedPort: 17893, Socket: filepath.Join(dir, "core.sock"), Resources: dir, State: t.TempDir()}
	h := host{euid: func() int { return 0 }, prepare: func(Options) error { return nil }, copyRules: func(Options) error { return nil }, cleanup: func(context.Context, int) error { return nil }, listen: func(o Options) (net.Listener, error) { return bindSocket(o, func(string) error { return nil }) }, readOwner: func(int) (processidentity.Identity, error) {
		return processidentity.Identity{PID: 123, UID: 501, Seconds: 10}, nil
	}, alive: func(processidentity.Identity) bool { return false }, ticks: ticks}
	err := run(ctx, o, r, singbox.ValidateCoreOptions, h)
	if failure == "persistent" {
		if err == nil || err.Error() != "core_clear_failed" {
			t.Fatal("missing bounded shutdown failure")
		}
	} else if err != nil {
		t.Fatal(err)
	}
	wantClears := 1
	if failure != "none" {
		wantClears = 2
	}
	if len(r.calls) != wantClears+1 || r.calls[0] != "clear" || r.calls[wantClears] != "close" || wantClears == 2 && r.calls[1] != "clear" {
		t.Fatal(r.calls)
	}
	if _, err := os.Lstat(o.Socket); !os.IsNotExist(err) {
		t.Fatal("socket remains")
	}
}

// The production CoreRuntime must yield the same safe reason as the RPC fixture.
// A loopback listener reproduces resource collision without root or host network writes.
func TestStartedServicePortCollisionReason(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	runtime := singbox.NewCoreRuntime(context.Background())
	defer runtime.Close()
	s := testServer(t, runtime)
	profile := fmt.Sprintf(`{"inbounds":[{"type":"mixed","listen":"127.0.0.1","listen_port":%d}],"outbounds":[{"type":"direct","tag":"direct"}]}`, listener.Addr().(*net.TCPAddr).Port)
	_, err = s.ApplyProfile(context.Background(), &pb.ApplyProfileRequest{ProfileJson: profile})
	if got := status.Convert(err).Message(); got != "core_profile_start_failed: address_in_use" {
		t.Fatalf("actual StartedService reason=%q", got)
	}
	if _, err := s.ClearProfile(context.Background(), &emptypb.Empty{}); err != nil {
		t.Fatal(err)
	}
}
