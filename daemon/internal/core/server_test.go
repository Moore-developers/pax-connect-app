package core

import (
	"context"
	"crypto/sha256"
	"fmt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"os"
	"path/filepath"
	pb "pax-connect/daemon/gen/proto/corev1"
	"pax-connect/daemon/internal/singbox"
	"reflect"
	"testing"
	"time"
)

type fakeRuntime struct {
	calls         []string
	state         string
	failClear     bool
	clearFailures int
	failApply     error
	profile       string
	failures      []string
}

func (*fakeRuntime) Register(grpc.ServiceRegistrar) {}
func (r *fakeRuntime) Apply(profile string) error {
	r.profile = profile
	if r.failApply != nil {
		return r.failApply
	}
	r.calls = append(r.calls, "apply")
	r.state = "STARTED"
	return nil
}
func (r *fakeRuntime) Clear() error {
	r.calls = append(r.calls, "clear")
	if r.clearFailures > 0 {
		r.clearFailures--
		return fmt.Errorf("transient clear failure")
	}
	if r.failClear {
		return fmt.Errorf("test failure")
	}
	r.state = "IDLE"
	return nil
}
func (r *fakeRuntime) RecordFailure(detail string) { r.failures = append(r.failures, detail) }
func (r *fakeRuntime) Status() string              { return r.state }
func (r *fakeRuntime) Close()                      { r.calls = append(r.calls, "close") }
func testServer(t *testing.T, r Runtime) *server {
	t.Helper()
	s, err := newServer(Options{OwnerPID: 123, MixedPort: 17893, Version: "test", Resources: t.TempDir(), State: t.TempDir()}, r, singbox.ValidateCoreOptions)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestHelloFingerprint(t *testing.T) {
	s := testServer(t, &fakeRuntime{state: "IDLE"})
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.Hello(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if h.Fingerprint != fmt.Sprintf("%x", sha256.Sum256(data)) || h.OwnerPid != 123 || h.ProtocolVersion != 6 || h.CoreVersion != "test" || h.ServiceStatus != "IDLE" {
		t.Fatalf("bad hello: %v", h)
	}
	file := filepath.Join(t.TempDir(), "binary")
	if err = os.WriteFile(file, []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := fingerprintFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, []byte("two"), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := fingerprintFile(file)
	if err != nil || a == b {
		t.Fatal("fingerprint did not change")
	}
}
func TestApplyRejectHasNoSideEffects(t *testing.T) {
	r := &fakeRuntime{state: "STARTED"}
	s := testServer(t, r)
	for _, raw := range []string{`{`, `{"log":{"output":"test"}}`, `{"outbounds":[{"type":"bad"}]}`} {
		_, err := s.ApplyProfile(context.Background(), &pb.ApplyProfileRequest{ProfileJson: raw, Revision: 7})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatal(err)
		}
		if len(r.calls) != 0 || r.state != "STARTED" {
			t.Fatal("reject mutated runtime")
		}
	}
	ack, err := s.ApplyProfile(context.Background(), &pb.ApplyProfileRequest{ProfileJson: `{}`, Revision: 9})
	if err != nil || ack.Revision != 9 {
		t.Fatalf("%v %v", ack, err)
	}
}
func TestClearAndShutdownOrdering(t *testing.T) {
	r := &fakeRuntime{state: "STARTED"}
	s := testServer(t, r)
	if _, err := s.ClearProfile(context.Background(), &emptypb.Empty{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.done:
		t.Fatal("clear stopped core")
	default:
	}
	r.failClear = true
	if _, err := s.Shutdown(context.Background(), &emptypb.Empty{}); status.Code(err) != codes.Internal {
		t.Fatal(err)
	}
	select {
	case <-s.done:
		t.Fatal("failed clear acknowledged shutdown")
	default:
	}
	r.failClear = false
	if _, err := s.Shutdown(context.Background(), &emptypb.Empty{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.done:
	default:
		t.Fatal("shutdown not requested")
	}
	if err := s.stop(); err != nil {
		t.Fatal(err)
	}
	r.Close()
	if !reflect.DeepEqual(r.calls, []string{"clear", "clear", "clear", "close"}) {
		t.Fatal(r.calls)
	}
	if _, err := s.ApplyProfile(context.Background(), &pb.ApplyProfileRequest{ProfileJson: `{}`}); status.Code(err) != codes.Unavailable {
		t.Fatal(err)
	}
}
func TestAllowlist(t *testing.T) {
	allowed := []string{"SubscribeServiceStatus", "SubscribeLog", "SubscribeStatus", "SubscribeGroups", "SubscribeOutbounds", "SelectOutbound", "URLTest", "SubscribeConnections", "CloseConnection", "CloseAllConnections", "GetStartedAt"}
	for _, name := range allowed {
		if !allowMethod("/daemon.StartedService/" + name) {
			t.Fatal(name)
		}
	}
	for _, name := range []string{"StopService", "ReloadService", "TriggerDebugCrash", "SetSystemProxyEnabled", "GetSystemProxyStatus", "SubscribeTailscaleStatus", "TailscaleLogout", "StartTailscaleSSHSession", "NewFutureMethod"} {
		m := "/daemon.StartedService/" + name
		_, err := unaryAllow(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: m}, func(context.Context, any) (any, error) { t.Fatal("called denied method"); return nil, nil })
		if status.Code(err) != codes.PermissionDenied {
			t.Fatal(err)
		}
		err = streamAllow(nil, nil, &grpc.StreamServerInfo{FullMethod: m}, func(any, grpc.ServerStream) error { t.Fatal("called denied stream"); return nil })
		if status.Code(err) != codes.PermissionDenied {
			t.Fatal(err)
		}
	}
	for _, m := range []string{"/evil.StartedService/SelectOutbound", "/daemon.StartedService/SelectOutbound/extra", "/pax.core.v1.PaxCore/NewFutureMethod"} {
		if allowMethod(m) {
			t.Fatal(m)
		}
	}
}
func TestWatchdogOwnerDeath(t *testing.T) {
	for _, reason := range []string{"owner death", "signal", "shutdown"} {
		t.Run(reason, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ticks := make(chan time.Time, 1)
			done := make(chan struct{})
			served := make(chan error)
			called := false
			switch reason {
			case "owner death":
				ticks <- time.Now()
			case "signal":
				cancel()
			case "shutdown":
				close(done)
			}
			if err := waitOwner(ctx, done, ticks, func() bool { called = true; return false }, served); err != nil {
				t.Fatal(err)
			}
			if reason == "owner death" && !called {
				t.Fatal("owner not checked")
			}
		})
	}
}

func TestStartFailureScrubbedAndRecorded(t *testing.T) {
	for _, tt := range []struct{ cause, want string }{
		{"listen tcp /private/user/home/file: address already in use user:fake-password@example.invalid", "address_in_use"},
		{"open /etc/private: permission denied fake-credential", "permission_denied"},
		{"open /tmp/private: no such file fake-credential", "resource_unavailable"},
		{"invalid secret fake-credential at /Users/private/node", "configuration_rejected"},
		{"opaque fake-credential /etc/private", "kernel_start_failed"},
	} {
		r := &fakeRuntime{failApply: fmt.Errorf("%s", tt.cause)}
		s := testServer(t, r)
		_, err := s.ApplyProfile(context.Background(), &pb.ApplyProfileRequest{ProfileJson: `{}`})
		want := "core_profile_start_failed: " + tt.want
		if status.Code(err) != codes.FailedPrecondition || status.Convert(err).Message() != want {
			t.Fatal("failure did not use safe vocabulary")
		}
		if len(r.failures) != 1 || r.failures[0] != want || len(want) > 256 {
			t.Fatal("scrubbed failure not recorded")
		}
	}
}

func TestRouteConflictIsNotReportedAsBadLineConfiguration(t *testing.T) {
	s := testServer(t, &fakeRuntime{failApply: fmt.Errorf("configure tun: add route: file exists")})
	_, err := s.ApplyProfile(context.Background(), &pb.ApplyProfileRequest{ProfileJson: `{}`})
	if got := status.Convert(err).Message(); got != "core_profile_start_failed: route_conflict" {
		t.Fatalf("route collision misreported: %s", got)
	}
}
