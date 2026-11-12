package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/network"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	pb "pax-connect/daemon/gen/proto/corev1"
)

// Runtime is the core's isolated sing-box boundary. Core serializes transitions.
type Runtime interface {
	Register(grpc.ServiceRegistrar)
	Apply(string) error
	Clear() error
	Status() string
	Close()
	RecordFailure(string)
}

// Options describes the root core's immutable launch identity and paths.
type Options struct {
	Replace                           bool
	OwnerPID, OwnerUID                int
	Socket, Resources, State, Version string
	MixedPort                         int
}

type server struct {
	pb.UnimplementedPaxCoreServer
	mu            sync.Mutex
	opts          Options
	runtime       Runtime
	parse         ProfileValidator
	fingerprint   string
	identify      func(context.Context, string) (tunOwner, error)
	releaseProxy  func(context.Context) error
	done          chan struct{}
	stopping      bool
	powerSequence uint64
	conflict      func(context.Context, string) network.ConflictResult
}

func newServer(o Options, r Runtime, parse ProfileValidator) (*server, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	fingerprint, err := fingerprintFile(exe)
	if err != nil {
		return nil, err
	}
	return &server{opts: o, runtime: r, parse: parse, fingerprint: fingerprint, identify: nativeIdentify, done: make(chan struct{})}, nil
}
func fingerprintFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (s *server) Hello(context.Context, *emptypb.Empty) (*pb.HelloResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	response := &pb.HelloResponse{ProtocolVersion: 6, CoreVersion: s.opts.Version, Fingerprint: s.fingerprint, OwnerPid: int32(s.opts.OwnerPID), ServiceStatus: s.runtime.Status()}
	if runtime, ok := s.runtime.(interface{ CurrentPathIdentity() (string, uint64) }); ok {
		response.RuntimeSession, response.RuntimeVersion = runtime.CurrentPathIdentity()
	}
	if runtime, ok := s.runtime.(interface{ Interface() string }); ok {
		response.Interface = runtime.Interface()
	}
	if runtime, ok := s.runtime.(interface{ SelectedTag() string }); ok {
		response.SelectedTag = runtime.SelectedTag()
	}
	return response, nil
}
func (s *server) ApplyProfile(ctx context.Context, req *pb.ApplyProfileRequest) (*pb.ApplyProfileResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return nil, status.Error(codes.Unavailable, "core_stopping")
	}
	profile, err := s.parse(ctx, []byte(req.ProfileJson), s.opts.State, s.opts.MixedPort)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err = ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if err = s.checkConflict(ctx); err != nil {
		return nil, err
	}
	if err = s.configureRoutingSources(req.RoutingSources); err != nil {
		return nil, err
	}
	if err = s.runtime.Apply(profile); err != nil {
		detail := startFailureDetail(err)
		s.runtime.RecordFailure("core_profile_start_failed: " + detail)
		return nil, status.Error(codes.FailedPrecondition, "core_profile_start_failed: "+detail)
	}
	return &pb.ApplyProfileResponse{Revision: req.Revision}, nil
}
func (s *server) ClearProfile(ctx context.Context, _ *emptypb.Empty) (*emptypb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.clearRuntime(ctx); err != nil {
		return nil, status.Error(codes.Internal, "core_clear_failed")
	}
	return &emptypb.Empty{}, nil
}
func (s *server) Shutdown(ctx context.Context, _ *emptypb.Empty) (*emptypb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.stopping {
		if err := s.clearRuntime(ctx); err != nil {
			return nil, status.Error(codes.Internal, "core_clear_failed")
		}
		s.stopping = true
		close(s.done)
	}
	return &emptypb.Empty{}, nil
}
func (s *server) stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return nil
	}
	s.stopping = true
	err := s.clearRuntime(context.Background())
	if err != nil {
		err = s.clearRuntime(context.Background())
	}
	return err
}

// Once closing starts, cancellation of a client RPC must not strand OS proxy
// settings. The root owner bounds this final cleanup independently of the client.
func (s *server) clearRuntime(ctx context.Context) error {
	if err := s.runtime.Clear(); err != nil {
		return err
	}
	if s.runtime.Status() != "IDLE" {
		return errors.New("core_clear_unconfirmed")
	}
	if s.releaseProxy == nil {
		return nil
	}
	cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer done()
	return s.releaseProxy(cleanup)
}
func (s *server) IdentifyTUNOwner(ctx context.Context, req *pb.IdentifyTUNOwnerRequest) (*pb.IdentifyTUNOwnerResponse, error) {
	if !validUTUN(req.Interface) {
		return nil, status.Error(codes.InvalidArgument, "core_interface_invalid")
	}
	owner, err := s.identify(ctx, req.Interface)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "core_tun_owner_unavailable")
	}
	return &pb.IdentifyTUNOwnerResponse{Kind: owner.Kind, DisplayName: owner.Name, BundleId: owner.BundleID, ExecutablePath: owner.Path, Pid: int32(owner.PID)}, nil
}

var errMethodDenied = errors.New("core_method_denied")

func allowMethod(method string) bool {
	switch method {
	case pb.PaxCore_UpdateRouting_FullMethodName, pb.PaxCore_VerifyCurrentPath_FullMethodName, pb.PaxCore_ReadObservations_FullMethodName, pb.PaxCore_ReadUsage_FullMethodName, pb.PaxCore_SelectRoute_FullMethodName, pb.PaxCore_SetPaused_FullMethodName, pb.PaxCore_Hello_FullMethodName, pb.PaxCore_ApplyProfile_FullMethodName, pb.PaxCore_ClearProfile_FullMethodName, pb.PaxCore_IdentifyTUNOwner_FullMethodName, pb.PaxCore_Shutdown_FullMethodName:
		return true
	}
	// Exact service prefix prevents lookalike services from reaching a handler.
	const prefix = "/daemon.StartedService/"
	switch method {
	case prefix + "SubscribeServiceStatus", prefix + "SubscribeLog", prefix + "SubscribeStatus", prefix + "SubscribeGroups", prefix + "SubscribeOutbounds", prefix + "SelectOutbound", prefix + "URLTest", prefix + "SubscribeConnections", prefix + "CloseConnection", prefix + "CloseAllConnections", prefix + "GetStartedAt":
		return true
	}
	return false
}
func unaryAllow(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
	if !allowMethod(info.FullMethod) {
		return nil, status.Error(codes.PermissionDenied, errMethodDenied.Error())
	}
	return next(ctx, req)
}
func streamAllow(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, next grpc.StreamHandler) error {
	if !allowMethod(info.FullMethod) {
		return status.Error(codes.PermissionDenied, errMethodDenied.Error())
	}
	return next(srv, stream)
}

func (s *server) SelectRoute(ctx context.Context, req *pb.SelectRouteRequest) (*pb.SelectRouteResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return nil, status.Error(codes.Unavailable, "core_stopping")
	}
	runtime, ok := s.runtime.(interface {
		SelectRoute(context.Context, string, string) (string, bool, error)
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "core_selector_unavailable")
	}
	selected, closed, err := runtime.SelectRoute(ctx, req.ExpectedTag, req.OutboundTag)
	if err != nil && selected != req.OutboundTag {
		return nil, status.Error(codes.FailedPrecondition, "core_selection_failed")
	}
	return &pb.SelectRouteResponse{SelectedTag: selected, OldConnectionsClosed: closed}, nil
}
func (s *server) SetPaused(ctx context.Context, req *pb.SetPausedRequest) (*emptypb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return nil, status.Error(codes.Unavailable, "core_stopping")
	}
	if req.Sequence <= s.powerSequence {
		return &emptypb.Empty{}, nil
	}
	if r, ok := s.runtime.(interface{ SetPaused(bool) error }); ok {
		if err := r.SetPaused(req.Paused); err != nil {
			return nil, status.Error(codes.Unavailable, "core_power_failed")
		}
	}
	s.powerSequence = req.Sequence
	return &emptypb.Empty{}, nil
}

func (s *server) ReadUsage(ctx context.Context, _ *emptypb.Empty) (*pb.UsageSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, ok := s.runtime.(interface {
		ReadUsage(context.Context) config.UsageSnapshot
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "usage_unavailable")
	}
	v := runtime.ReadUsage(ctx)
	out := &pb.UsageSnapshot{Session: v.Session, Sequence: v.Sequence, SampledUnixMilli: v.At.UnixMilli(), Partial: v.Partial, ConnectionsKnown: v.ConnectionsKnown}
	for _, r := range v.Counters {
		out.Counters = append(out.Counters, &pb.UsageCounter{Tag: r.Tag, Upload: r.Upload, Download: r.Download, Connections: r.Connections})
	}
	return out, nil
}

// UpdateRouting admits only the existing safe profile schema; runtime verifies that
// listeners, outbounds, DNS and system ownership remain identical.
func (s *server) UpdateRouting(ctx context.Context, req *pb.ApplyProfileRequest) (*pb.ApplyProfileResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return nil, status.Error(codes.Unavailable, "core_stopping")
	}
	profile, err := s.parse(ctx, []byte(req.ProfileJson), s.opts.State, s.opts.MixedPort)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "routing_profile_invalid")
	}
	runtime, ok := s.runtime.(interface {
		UpdateRouting(context.Context, string) error
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "routing_update_unavailable")
	}
	if err := s.configureRoutingSources(req.RoutingSources); err != nil {
		return nil, err
	}
	if err := runtime.UpdateRouting(ctx, profile); err != nil {
		return nil, status.Error(codes.FailedPrecondition, "routing_update_rejected")
	}
	return &pb.ApplyProfileResponse{Revision: req.Revision}, nil
}

func (s *server) configureRoutingSources(values []string) error {
	if len(values) > 20000 {
		return status.Error(codes.InvalidArgument, "routing_sources_invalid")
	}
	total := 0
	for _, v := range values {
		total += len(v)
		if len(v) > 4096 || total > 1<<20 {
			return status.Error(codes.InvalidArgument, "routing_sources_invalid")
		}
	}
	if runtime, ok := s.runtime.(interface{ SetRoutingSources([]string) }); ok {
		runtime.SetRoutingSources(values)
	}
	return nil
}
