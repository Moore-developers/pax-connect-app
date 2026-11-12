package coreclient

import (
	"context"
	"testing"

	pb "pax-connect/daemon/gen/proto/corev1"
	"pax-connect/daemon/internal/config"
)

func TestCurrentPathRejectsChangedFrozenCoreIdentity(t *testing.T) {
	for _, q := range []config.CurrentPathRequest{
		{CheckID: "session", ExpectedTag: "node", ExpectedSession: "old", ExpectedVersion: 7},
		{CheckID: "version", ExpectedTag: "node", ExpectedSession: "current", ExpectedVersion: 6},
	} {
		t.Run(q.CheckID, func(t *testing.T) {
			f := &fixtureCore{hello: &pb.HelloResponse{ProtocolVersion: 6, Fingerprint: "fixture", OwnerPid: 123, ServiceStatus: "STARTED", SelectedTag: "node", RuntimeSession: "current", RuntimeVersion: 7}}
			s := fixtureSession(t, f)
			result, err := s.VerifyCurrentPath(context.Background(), q)
			if err != nil || result.State != "stale" || result.Cause != "current_path_stale" {
				t.Fatalf("changed frozen identity was not rejected: state=%s cause=%s err=%v", result.State, result.Cause, err)
			}
		})
	}
}
