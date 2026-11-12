package coreclient

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"
	pb "pax-connect/daemon/gen/proto/corev1"
	"pax-connect/daemon/internal/config"
)

type fixtureCore struct {
	pb.UnimplementedPaxCoreServer
	hello      *pb.HelloResponse
	profiles   []string
	attempts   int
	cleared    int
	applyErr   error
	selectMode string
	powers     []uint64
}

func (f *fixtureCore) Hello(context.Context, *emptypb.Empty) (*pb.HelloResponse, error) {
	return f.hello, nil
}
func (f *fixtureCore) ApplyProfile(_ context.Context, r *pb.ApplyProfileRequest) (*pb.ApplyProfileResponse, error) {
	f.attempts++
	if f.applyErr != nil {
		return nil, f.applyErr
	}
	f.profiles = append(f.profiles, r.ProfileJson)
	f.hello.ServiceStatus = "STARTED"
	return &pb.ApplyProfileResponse{Revision: r.Revision}, nil
}

func TestSessionPreservesScrubbedCoreStartFailureDetail(t *testing.T) {
	f := &fixtureCore{
		hello:    &pb.HelloResponse{ProtocolVersion: 6, Fingerprint: "fixture", OwnerPid: 123, ServiceStatus: "IDLE"},
		applyErr: status.Error(codes.FailedPrecondition, "core_profile_start_failed: address_in_use"),
	}
	s := fixtureSession(t, f)
	err := s.Apply(context.Background(), config.RouteConfig{})
	if err == nil || err.Error() != "core_profile_start_failed: address_in_use" {
		t.Fatalf("Apply error=%v, want scrubbed start detail", err)
	}
	if len(f.profiles) != 0 {
		t.Fatal("failed profile must not be reported as applied")
	}
}
func (f *fixtureCore) ClearProfile(context.Context, *emptypb.Empty) (*emptypb.Empty, error) {
	f.cleared++
	f.hello.ServiceStatus = "IDLE"
	return &emptypb.Empty{}, nil
}
func fixtureSession(t *testing.T, f *fixtureCore) *Session {
	t.Helper()
	l := bufconn.Listen(1024 * 1024)
	s := grpc.NewServer()
	pb.RegisterPaxCoreServer(s, f)
	go func() { _ = s.Serve(l) }()
	t.Cleanup(s.Stop)
	c, err := grpc.NewClient("passthrough:///fixture", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return l.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	r := New("fixture", 123, func(context.Context, config.RouteConfig) (string, error) { return `{"outbounds":[]}`, nil })
	r.connect = func(context.Context) (pb.PaxCoreClient, io.Closer, error) {
		return pb.NewPaxCoreClient(c), io.NopCloser(nil), nil
	}
	return r
}
func TestSessionRejectsIdentityBeforeSendingProfile(t *testing.T) {
	for _, hello := range []*pb.HelloResponse{
		{ProtocolVersion: 99, Fingerprint: "fixture", OwnerPid: 123, ServiceStatus: "IDLE"},
		{ProtocolVersion: 6, Fingerprint: "wrong", OwnerPid: 123, ServiceStatus: "IDLE"},
		{ProtocolVersion: 6, Fingerprint: "fixture", OwnerPid: 456, ServiceStatus: "IDLE"},
	} {
		t.Run(hello.String(), func(t *testing.T) {
			f := &fixtureCore{hello: hello}
			s := fixtureSession(t, f)
			if err := s.Apply(context.Background(), config.RouteConfig{}); err == nil {
				t.Fatal("identity accepted")
			}
			if len(f.profiles) != 0 {
				t.Fatal("sent credentials to mismatched core")
			}
		})
	}
}
func TestSessionClearsFatalBeforeReapplyingSelectedProfile(t *testing.T) {
	f := &fixtureCore{hello: &pb.HelloResponse{ProtocolVersion: 6, Fingerprint: "fixture", OwnerPid: 123, ServiceStatus: "FATAL"}}
	s := fixtureSession(t, f)
	if err := s.Apply(context.Background(), config.RouteConfig{ResolvedConfigRevision: 7}); err != nil {
		t.Fatal(err)
	}
	if len(f.profiles) != 1 || f.cleared != 1 {
		t.Fatal("fatal instance not retired and reapplied")
	}
	if err := s.Clear(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestMissingCoreRequiresAuthorizationButClearIsHarmless(t *testing.T) {
	s := New("fixture", 123, nil)
	s.connect = func(context.Context) (pb.PaxCoreClient, io.Closer, error) { return nil, nil, ErrRequired }
	if !errors.Is(s.Apply(context.Background(), config.RouteConfig{}), ErrRequired) {
		t.Fatal("missing core must request authorization")
	}
	if err := s.Clear(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSessionRejectsNonAllowlistedCoreFailureText(t *testing.T) {
	for _, message := range []string{"core_profile_start_failed: secret_token", "core_profile_start_failed: https://private.invalid/token", "upstream private text"} {
		err := scrubbedStartFailure(status.Error(codes.FailedPrecondition, message))
		if err.Error() != "core_profile_start_failed" {
			t.Fatal("private or unknown reason escaped")
		}
	}
}

func (f *fixtureCore) SelectRoute(_ context.Context, r *pb.SelectRouteRequest) (*pb.SelectRouteResponse, error) {
	if f.selectMode == "lost_ack" {
		f.hello.SelectedTag = r.OutboundTag
		return nil, status.Error(codes.Unavailable, "fixture lost ack")
	}
	if f.selectMode == "failed" {
		return nil, status.Error(codes.FailedPrecondition, "fixture failed")
	}
	f.hello.SelectedTag = r.OutboundTag
	return &pb.SelectRouteResponse{SelectedTag: r.OutboundTag, OldConnectionsClosed: true}, nil
}
func (f *fixtureCore) SetPaused(_ context.Context, r *pb.SetPausedRequest) (*emptypb.Empty, error) {
	f.powers = append(f.powers, r.Sequence)
	return &emptypb.Empty{}, nil
}
func TestSwitchReconcilesLostAcknowledgementWithActualCoreSelection(t *testing.T) {
	for _, mode := range []string{"success", "failed", "lost_ack"} {
		t.Run(mode, func(t *testing.T) {
			a := config.RouteConfig{NodeProfile: config.NodeProfile{ID: "a"}, ResolvedConfigRevision: 1}
			b := a
			b.ID = "b"
			f := &fixtureCore{hello: &pb.HelloResponse{ProtocolVersion: 6, Fingerprint: "fixture", OwnerPid: 123, ServiceStatus: "STARTED", SelectedTag: config.RuntimeTag(a)}, selectMode: mode}
			s := fixtureSession(t, f)
			s.selected = config.RuntimeTag(a)
			s.tags = map[string]bool{config.RuntimeTag(a): true, config.RuntimeTag(b): true}
			committed, err := s.SwitchRoute(context.Background(), b)
			if mode == "failed" {
				if committed || err == nil || s.selected != config.RuntimeTag(a) {
					t.Fatal("failed selection committed")
				}
			} else {
				if !committed || s.selected != config.RuntimeTag(b) {
					t.Fatal("actual core selection lost", err)
				}
				if (err != nil) != (mode == "lost_ack") {
					t.Fatal("lost ack not reported as pending cleanup")
				}
			}
		})
	}
}
func TestSessionPowerForwardsSequenceWithoutStartingIdleCore(t *testing.T) {
	f := &fixtureCore{hello: &pb.HelloResponse{ProtocolVersion: 6, Fingerprint: "fixture", OwnerPid: 123, ServiceStatus: "IDLE"}}
	s := fixtureSession(t, f)
	if err := s.Power(context.Background(), false, 1); err != nil {
		t.Fatal(err)
	}
	if len(f.powers) != 0 || len(f.profiles) != 0 {
		t.Fatal("wake started idle core")
	}
	f.hello.ServiceStatus = "STARTED"
	if err := s.Power(context.Background(), true, 2); err != nil {
		t.Fatal(err)
	}
	if len(f.powers) != 1 || f.powers[0] != 2 {
		t.Fatal(f.powers)
	}
}

func TestOffThenFailedOnNeverRestoresPreviousRoute(t *testing.T) {
	f := &fixtureCore{hello: &pb.HelloResponse{ProtocolVersion: 6, Fingerprint: "fixture", OwnerPid: 123, ServiceStatus: "IDLE"}}
	s := fixtureSession(t, f)
	a := config.RouteConfig{NodeProfile: config.NodeProfile{ID: "a"}, ResolvedConfigRevision: 1}
	if err := s.ApplyCatalog(context.Background(), []config.RouteConfig{a}, a); err != nil {
		t.Fatal(err)
	}
	if err := s.Clear(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.applyErr = status.Error(codes.FailedPrecondition, "core_profile_start_failed: configuration_rejected")
	b := a
	b.ID = "b"
	if err := s.ApplyCatalog(context.Background(), []config.RouteConfig{b}, b); err == nil {
		t.Fatal("failed ON accepted")
	}
	if f.attempts != 2 {
		t.Fatalf("failed ON attempted to restore previously closed route: Apply calls=%d", f.attempts)
	}
	if f.hello.ServiceStatus != "IDLE" {
		t.Fatal("old route revived")
	}
}

func TestFailedOnDoesNotRestoreProfileFromDeadService(t *testing.T) {
	f := &fixtureCore{hello: &pb.HelloResponse{ProtocolVersion: 6, Fingerprint: "fixture", OwnerPid: 123, ServiceStatus: "IDLE"}}
	s := fixtureSession(t, f)
	a := config.RouteConfig{NodeProfile: config.NodeProfile{ID: "a"}, ResolvedConfigRevision: 1}
	if err := s.ApplyCatalog(context.Background(), []config.RouteConfig{a}, a); err != nil {
		t.Fatal(err)
	}
	f.hello.ServiceStatus = "FATAL"
	f.applyErr = status.Error(codes.FailedPrecondition, "core_profile_start_failed: configuration_rejected")
	b := a
	b.ID = "b"
	if err := s.ApplyCatalog(context.Background(), []config.RouteConfig{b}, b); err == nil {
		t.Fatal("failed ON accepted")
	}
	if f.attempts != 2 {
		t.Fatal("restored a dead service")
	}
}

func TestReadRoutingRulesUsesCommittedProfileAndNeverStartsCore(t *testing.T) {
	f := &fixtureCore{hello: &pb.HelloResponse{ProtocolVersion: 6, Fingerprint: "fixture", OwnerPid: 123, ServiceStatus: "IDLE"}}
	s := fixtureSession(t, f)
	first, err := s.ReadRoutingRules(context.Background())
	if err != nil || first.State != "not_running" || f.attempts != 0 {
		t.Fatal(first, err)
	}
	s.build = func(context.Context, config.RouteConfig) (string, error) {
		return `{"outbounds":[],"route":{"rules":[{"action":"sniff"},{"ip_is_private":true,"outbound":"direct"},{"rule_set":["geosite-cn"],"outbound":"direct"}],"final":"proxy"}}`, nil
	}
	if err = s.Apply(context.Background(), config.RouteConfig{}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadRoutingRules(context.Background())
	if err != nil || got.State != "active" || len(got.Rules) != 4 || got.Rules[1].Action != "直连" {
		t.Fatal(got, err)
	}
	f.hello.RuntimeSession = "replacement"
	replaced, e := s.ReadRoutingRules(context.Background())
	if e != nil || replaced.State != "unknown" || len(replaced.Rules) != 0 {
		t.Fatal("replacement core reused old profile", replaced, e)
	}
	f.hello.ServiceStatus = "IDLE"
	stale, err := s.ReadRoutingRules(context.Background())
	if err != nil || len(stale.Rules) != 0 || stale.State != "not_running" {
		t.Fatal("stale rule set shown active", stale, err)
	}
}
