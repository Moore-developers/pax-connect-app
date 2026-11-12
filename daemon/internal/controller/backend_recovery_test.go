package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

type recoveryCore struct {
	fakeCoreSession
	observation config.CoreObservation
	failure     error
}

func (f *recoveryCore) ObserveCore(context.Context) (config.CoreObservation, error) {
	return f.observation, f.failure
}

func TestBackendRestartReportsExistingCoreWithoutMutatingIt(t *testing.T) {
	for _, scenario := range []struct {
		name, state string
		failure     error
		want        pb.ProtectionStatus
	}{
		{"running", "STARTED", nil, pb.ProtectionStatus_PROTECTION_STATUS_DEGRADED},
		{"idle", "IDLE", nil, pb.ProtectionStatus_PROTECTION_STATUS_OFF},
		{"missing", "", errors.New("core_required"), pb.ProtectionStatus_PROTECTION_STATUS_OFF},
		{"identity", "", errors.New("core_identity_mismatch"), pb.ProtectionStatus_PROTECTION_STATUS_DEGRADED},
		{"unresponsive", "", errors.New("core_unavailable"), pb.ProtectionStatus_PROTECTION_STATUS_DEGRADED},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			c := New("test", "fixture")
			core := &recoveryCore{observation: config.CoreObservation{Status: scenario.state, RuntimeSession: "existing", SelectedTag: "opaque"}, failure: scenario.failure}
			c.ConfigureUserMode(func() {}, 0)
			c.ConfigureCoreSession(core)
			c.ConfigureOperations(context.Background(), func() (config.DebugProfile, error) { return config.DebugProfile{}, config.ErrProfileUnavailable })
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = c.CloseProxy(ctx)
			})
			snapshot, err := c.GetSnapshot(context.Background(), &pb.GetSnapshotRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Status != scenario.want {
				t.Fatalf("startup status = %v, want %v", snapshot.Status, scenario.want)
			}
			if scenario.want == pb.ProtectionStatus_PROTECTION_STATUS_DEGRADED && (snapshot.GetDegradedReason().GetCode() != "runtime_observation_unavailable" || snapshot.CurrentRoute != nil) {
				t.Fatalf("unsafe recovered binding: %v", snapshot)
			}
			core.mu.Lock()
			mutations := len(core.profiles) + core.clears + core.shutdowns
			core.mu.Unlock()
			if mutations != 0 {
				t.Fatalf("startup changed core: %d", mutations)
			}
		})
	}
}

func TestBackendHelloReportsAppOwner(t *testing.T) {
	c := New("test", "fixture")
	c.ConfigureUserMode(func() {}, 12345)
	hello, err := c.Hello(context.Background(), &pb.HelloRequest{ProtocolVersion: ProtocolVersion})
	if err != nil || hello.GetOwnerAppPid() != 12345 {
		t.Fatalf("owner handshake: %v %v", hello, err)
	}
}
