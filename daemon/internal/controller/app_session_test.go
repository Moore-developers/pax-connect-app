package controller

import (
	"context"
	"errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"strings"
	"testing"
)

func TestShutdownAppSession(t *testing.T) {
	c := New("test", "fixture")
	if _, err := c.ShutdownAppSession(context.Background(), &emptypb.Empty{}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("external daemon must reject shutdown: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.ConfigureUserMode(cancel, 0)
	for i := 0; i < 2; i++ {
		if _, err := c.ShutdownAppSession(context.Background(), &emptypb.Empty{}); err != nil {
			t.Fatal(err)
		}
	}
	if ctx.Err() == nil {
		t.Fatal("session not drained")
	}
}

func TestShutdownKeepsSessionWhenNetworkRestoreFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := New("test", "")
	runtime := &shutdownFailCore{failure: errors.New("fixture restore failed")}
	c.ConfigureOperations(ctx,
		func() (config.DebugProfile, error) { return config.DebugProfile{}, nil })
	c.ConfigureUserMode(cancel, 0)
	c.ConfigureCoreSession(runtime)
	_, err := c.ShutdownAppSession(context.Background(), &emptypb.Empty{})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("missing restore failure: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("failed restoration still stopped session")
	}
}

func TestShutdownRetriesRestoreBeforeExiting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := New("test", "")
	runtime := &shutdownFailCore{failure: errors.New("fixture restore failed")}
	c.ConfigureOperations(ctx, func() (config.DebugProfile, error) { return config.DebugProfile{}, nil })
	c.ConfigureUserMode(cancel, 0)
	c.ConfigureCoreSession(runtime)
	if _, err := c.ShutdownAppSession(context.Background(), &emptypb.Empty{}); err == nil {
		t.Fatal("expected first failure")
	}
	// The owner has finished, so mutating this fixture cannot race its Stop.
	runtime.failure = nil
	if _, err := c.ShutdownAppSession(context.Background(), &emptypb.Empty{}); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() == nil {
		t.Fatal("successful retry did not stop session")
	}
}

func TestUserModeHelloAndPowerOperations(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	stopped := false
	c.ConfigureUserMode(func() { stopped = true }, 0)
	hello, err := c.Hello(context.Background(), &paxv1.HelloRequest{ProtocolVersion: ProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	capabilities := strings.Join(hello.Capabilities, ",")
	if !strings.Contains(capabilities, "paxd.user") || !strings.Contains(capabilities, "proxy.power") || strings.Contains(capabilities, "app.session") {
		t.Fatal(capabilities)
	}
	results, stop := operationSink(c)
	defer stop()
	// No selected route, root privileges or working runtime is needed for core_required.
	for _, enabled := range []bool{true, false, true, false} {
		ack, err := c.SetProxyEnabled(context.Background(), &paxv1.SetProxyEnabledRequest{Enabled: enabled})
		if err != nil || ack.OperationId == "" {
			t.Fatalf("missing ACK: %v", err)
		}
		result := awaitResult(t, results, ack.OperationId)
		if enabled && result.Error.GetCode() != "core_required" || !enabled && !result.Ok {
			t.Fatal(result)
		}
		if waitComplete(t, c).Status != paxv1.ProtectionStatus_PROTECTION_STATUS_OFF {
			t.Fatal("user paxd started runtime")
		}
	}
	if _, err := c.ShutdownAppSession(context.Background(), &emptypb.Empty{}); err != nil || !stopped {
		t.Fatalf("user shutdown %v", err)
	}
}

type shutdownFailCore struct {
	fakeCoreSession
	failure error
}

func (f *shutdownFailCore) Shutdown(context.Context) error { return f.failure }
