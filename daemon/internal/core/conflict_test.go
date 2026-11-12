package core

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"
	pb "pax-connect/daemon/gen/proto/corev1"
	"pax-connect/daemon/internal/network"
)

func TestApplyIdentifiesExternalTUNBeforeAnyRuntimeMutation(t *testing.T) {
	for _, scenario := range []struct {
		name, kind, owner string
		denied            bool
	}{
		{"network extension", "network_extension", "Example VPN", false},
		{"process", "process", "v2rayN", false},
		{"unknown", "unknown", "", false},
		{"permission denied", "unknown", "", true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			runtime := &fakeRuntime{state: "IDLE"}
			s := testServer(t, runtime)
			s.conflict = func(context.Context, string) network.ConflictResult {
				return network.ConflictResult{Kind: network.ConflictOtherUTUN, Interface: "utun42"}
			}
			s.identify = func(context.Context, string) (tunOwner, error) {
				if scenario.denied {
					return tunOwner{}, errors.New("permission denied")
				}
				return tunOwner{Kind: scenario.kind, Name: scenario.owner}, nil
			}
			_, err := s.ApplyProfile(context.Background(), &pb.ApplyProfileRequest{ProfileJson: `{}`})
			if status.Convert(err).Message() != "tun_conflict_other" {
				t.Fatal(err)
			}
			found := false
			for _, detail := range status.Convert(err).Details() {
				if info, ok := detail.(*errdetails.ErrorInfo); ok {
					found = true
					if info.Metadata["kind"] != scenario.kind || info.Metadata["name"] != scenario.owner {
						t.Fatal(info)
					}
				}
			}
			if !found || len(runtime.calls) != 0 {
				t.Fatal("missing ownership or mutated runtime", runtime.calls)
			}
		})
	}
}
func TestUnknownNetworkObservationNeverStartsCoreProfile(t *testing.T) {
	runtime := &fakeRuntime{state: "IDLE"}
	s := testServer(t, runtime)
	s.conflict = func(context.Context, string) network.ConflictResult {
		return network.ConflictResult{Kind: network.ConflictObservationError}
	}
	_, err := s.ApplyProfile(context.Background(), &pb.ApplyProfileRequest{ProfileJson: `{}`})
	if status.Convert(err).Message() != "network_observation_unavailable" || len(runtime.calls) != 0 {
		t.Fatal("unknown observation accepted", err)
	}
}
