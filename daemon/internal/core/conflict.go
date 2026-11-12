package core

import (
	"context"
	"strings"
	"unicode"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"pax-connect/daemon/internal/network"
)

// Conflict observation is read-only and happens before the runtime can write
// system proxy settings. Unknown ownership never authorizes process control.
func (s *server) checkConflict(ctx context.Context) error {
	if s.conflict == nil {
		return nil
	}
	own := ""
	if runtime, ok := s.runtime.(interface{ Interface() string }); ok {
		own = runtime.Interface()
	}
	observed := s.conflict(ctx, own)
	if observed.Kind == network.ConflictNone {
		return nil
	}
	if observed.Kind == network.ConflictUnknown || observed.Kind == network.ConflictObservationError {
		return status.Error(codes.Unavailable, "network_observation_unavailable")
	}
	if observed.Kind == network.ConflictPax {
		if own != "" && observed.Interface == own {
			return nil
		}
		return status.Error(codes.FailedPrecondition, "pax_tun_stale")
	}
	name, kind := "", "unknown"
	if owner, err := s.identify(ctx, observed.Interface); err == nil {
		name = owner.Name
		kind = owner.Kind
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	if len(name) > 128 {
		name = ""
	}
	detail := &errdetails.ErrorInfo{Reason: "tun_conflict_other", Domain: "pax.core", Metadata: map[string]string{"name": name, "kind": kind, "interface": observed.Interface}}
	failure, err := status.New(codes.FailedPrecondition, "tun_conflict_other").WithDetails(detail)
	if err != nil {
		return status.Error(codes.FailedPrecondition, "tun_conflict_other")
	}
	return failure.Err()
}
