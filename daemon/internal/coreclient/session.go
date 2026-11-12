package coreclient

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"strings"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	pb "pax-connect/daemon/gen/proto/corev1"
	"pax-connect/daemon/internal/config"
)

const Socket = "/private/var/run/pax-core/core.sock"

var ErrRequired = errors.New("core_required")
var ErrUnavailable = errors.New("core_unavailable")
var ErrIdentity = errors.New("core_identity_mismatch")

// coreStartFailure preserves only the root core's bounded, scrubbed reason.
// The server never forwards upstream sing-box text; this error is safe to pass
// through the product operation queue as a code plus one presentation param.
type coreStartFailure struct{ detail string }

func (e *coreStartFailure) Error() string { return "core_profile_start_failed: " + e.detail }
func (e *coreStartFailure) ProductFailure() (string, []string) {
	return "core_profile_start_failed", []string{e.detail}
}

func scrubbedStartFailure(err error) error {
	const prefix = "core_profile_start_failed: "
	message := status.Convert(err).Message()
	if message == "network_observation_unavailable" || message == "pax_tun_stale" {
		return errors.New(message)
	}
	if message == "tun_conflict_other" {
		for _, detail := range status.Convert(err).Details() {
			if info, ok := detail.(*errdetails.ErrorInfo); ok && info.Domain == "pax.core" && info.Reason == message {
				return &tunConflict{params: []string{info.Metadata["name"], info.Metadata["kind"], info.Metadata["interface"]}}
			}
		}
		return errors.New(message)
	}
	if !strings.HasPrefix(message, prefix) {
		return errors.New("core_profile_start_failed")
	}
	detail := strings.TrimSpace(strings.TrimPrefix(message, prefix))
	switch detail {
	case "address_in_use", "permission_denied", "resource_unavailable", "configuration_rejected", "route_conflict", "kernel_start_failed":
	default:
		return errors.New("core_profile_start_failed")
	}
	return &coreStartFailure{detail: detail}
}

// Session authenticates each connection before transmitting private profile JSON.
// The caller serializes methods; connections never survive a core replacement.
type Session struct {
	applied             [32]byte
	hasApplied          bool
	fingerprint         string
	ownerPID            int
	build               func(context.Context, config.RouteConfig) (string, error)
	buildCatalog        func(context.Context, []config.RouteConfig, config.RouteConfig) (string, error)
	tags                map[string]bool
	selected            string
	pendingCatalog      []config.RouteConfig
	rulesRuntimeSession string
	lastProfile         string
	lastRoutingSources  []string
	lastWebRTC          bool
	lastDNSMode         string
	lastIPv4Only        bool
	lastRevision        uint64
	connect             func(context.Context) (pb.PaxCoreClient, io.Closer, error)
}

// New binds the expected signed bundle executable digest and App owner PID.
func New(fingerprint string, ownerPID int, build func(context.Context, config.RouteConfig) (string, error)) *Session {
	s := &Session{fingerprint: fingerprint, ownerPID: ownerPID, build: build}
	s.connect = func(ctx context.Context) (pb.PaxCoreClient, io.Closer, error) {
		// Establish missing/denied identity before gRPC's retry layer can flatten it.
		probe, err := dialRoot(ctx, "")
		if err != nil {
			return nil, nil, err
		}
		_ = probe.Close()
		conn, err := grpc.NewClient("passthrough:///pax-core", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(dialRoot))
		if err != nil {
			return nil, nil, ErrUnavailable
		}
		return pb.NewPaxCoreClient(conn), conn, nil
	}
	return s
}
func (s *Session) open(ctx context.Context) (pb.PaxCoreClient, io.Closer, string, error) {
	client, closer, err := s.connect(ctx)
	if err != nil {
		return nil, nil, "", err
	}
	probe, done := context.WithTimeout(ctx, time.Second)
	defer done()
	hello, err := client.Hello(probe, &emptypb.Empty{})
	if err != nil {
		_ = closer.Close()
		return nil, nil, "", ErrUnavailable
	}
	if hello.ProtocolVersion != 6 || hello.Fingerprint != s.fingerprint || s.fingerprint == "" || int(hello.OwnerPid) != s.ownerPID {
		_ = closer.Close()
		return nil, nil, "", ErrIdentity
	}
	switch hello.ServiceStatus {
	case "IDLE", "STARTED", "FATAL":
	default:
		_ = closer.Close()
		return nil, nil, "", ErrUnavailable
	}
	return client, closer, hello.ServiceStatus, nil
}

// Ready performs a read-only Hello check; it never starts or reloads a profile.
func (s *Session) Ready(ctx context.Context) error {
	_, closer, _, err := s.open(ctx)
	if err == nil {
		_ = closer.Close()
	}
	return err
}

// Apply re-establishes configuration from paxd's selected profile, including after FATAL.
func (s *Session) Apply(ctx context.Context, p config.RouteConfig) error {
	c, closer, state, err := s.open(ctx)
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			return ErrRequired
		}
		return err
	}
	defer func() { _ = closer.Close() }()
	if state == "FATAL" {
		if _, err = c.ClearProfile(ctx, &emptypb.Empty{}); err != nil {
			return ErrUnavailable
		}
	}
	var profile string
	if s.buildCatalog != nil && s.pendingCatalog != nil {
		profile, err = s.buildCatalog(ctx, s.pendingCatalog, p)
	} else {
		profile, err = s.build(ctx, p)
	}
	if err != nil {
		return errors.New("config_invalid")
	}
	digest := sha256.Sum256([]byte(profile))
	if state == "STARTED" && s.hasApplied && s.applied == digest {
		return nil
	}
	// A lost acknowledgement can still leave the core running. Retain this
	// uncertainty until a successful Clear; never turn it into an OFF success.
	s.hasApplied = true
	var sources []string
	if p.Routing != nil {
		tags := map[string]string{}
		for _, r := range append(s.pendingCatalog, p) {
			tags["route:"+r.ID] = config.RuntimeTag(r)
		}
		_, sources, err = config.CompileRoutingEvidence(*p.Routing, p.RoutingApplications, tags, !p.IPv4Only)
		if err != nil {
			return err
		}
	}
	ack, err := c.ApplyProfile(ctx, &pb.ApplyProfileRequest{ProfileJson: profile, Revision: p.ResolvedConfigRevision, RoutingSources: sources})
	if err != nil {
		return scrubbedStartFailure(err)
	}
	if ack.Revision != p.ResolvedConfigRevision {
		return ErrUnavailable
	}
	hello, err := c.Hello(ctx, &emptypb.Empty{})
	if err != nil || hello.ServiceStatus != "STARTED" {
		return ErrUnavailable
	}
	s.lastProfile = profile
	s.lastRoutingSources = sources
	s.lastWebRTC = p.NetworkPrivacy.WebRTC
	s.lastDNSMode = p.NetworkPrivacy.DNSMode
	s.lastIPv4Only = p.IPv4Only
	s.rulesRuntimeSession = hello.RuntimeSession
	s.lastRevision = p.ResolvedConfigRevision
	s.applied = digest
	s.hasApplied = true
	s.selected = config.RuntimeTag(p)
	s.tags = map[string]bool{s.selected: true}
	for _, node := range s.pendingCatalog {
		s.tags[config.RuntimeTag(node)] = true
	}
	return nil
}

// Clear releases network resources while retaining the core process.
func (s *Session) Clear(ctx context.Context) error {
	c, closer, _, err := s.open(ctx)
	if errors.Is(err, ErrRequired) && !s.hasApplied {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = closer.Close() }()
	if _, err = c.ClearProfile(ctx, &emptypb.Empty{}); err != nil {
		return ErrUnavailable
	}
	s.hasApplied = false
	s.lastProfile = ""
	s.lastRoutingSources = nil
	s.rulesRuntimeSession = ""
	s.lastRevision = 0
	s.applied = [32]byte{}
	s.selected = ""
	s.tags = nil
	return nil
}

// Shutdown drains this App's core during paxd shutdown, including while OFF.
func (s *Session) Shutdown(ctx context.Context) error {
	c, closer, _, err := s.open(ctx)
	if errors.Is(err, ErrRequired) && !s.hasApplied {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = closer.Close() }()
	if _, err = c.Shutdown(ctx, &emptypb.Empty{}); err != nil {
		return ErrUnavailable
	}
	return nil
}

// ConfigureCatalogBuilder supplies the private full-catalog builder at composition.
func (s *Session) ConfigureCatalogBuilder(build func(context.Context, []config.RouteConfig, config.RouteConfig) (string, error)) {
	s.buildCatalog = build
}

// ApplyCatalog freezes the route set for this instance. A subsequent structural
// catalog change uses this same explicit reload path, never a second TUN owner.
func (s *Session) ApplyCatalog(ctx context.Context, routes []config.RouteConfig, p config.RouteConfig) error {
	previousSources := s.lastRoutingSources
	previous, revision, tag, tags, digest := s.lastProfile, s.lastRevision, s.selected, s.tags, s.applied
	if previous != "" {
		_, closer, state, err := s.open(ctx)
		if err != nil {
			return err
		}
		_ = closer.Close()
		if state != "STARTED" {
			previous = ""
		}
	}
	s.pendingCatalog = routes
	defer func() { s.pendingCatalog = nil }()
	err := s.Apply(ctx, p)
	if err == nil || previous == "" {
		return err
	}
	// A catalog change replaces structure. If it fails, explicitly restore the
	// last known profile and selection, rather than claim the old TUN survived.
	recovery, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer done()
	client, closer, _, openErr := s.open(recovery)
	if openErr != nil {
		return errors.New("core_selection_unknown")
	}
	defer func() { _ = closer.Close() }()
	hello, readErr := client.Hello(recovery, &emptypb.Empty{})
	if readErr == nil && hello.ServiceStatus == "STARTED" && hello.SelectedTag == tag {
		return err
	}
	if ctx.Err() != nil {
		return errors.New("core_selection_unknown")
	}
	if _, clearErr := client.ClearProfile(recovery, &emptypb.Empty{}); clearErr != nil {
		return errors.New("core_selection_unknown")
	}
	if _, restoreErr := client.ApplyProfile(recovery, &pb.ApplyProfileRequest{ProfileJson: previous, Revision: revision, RoutingSources: previousSources}); restoreErr != nil {
		return errors.New("core_selection_unknown")
	}
	hello, readErr = client.Hello(recovery, &emptypb.Empty{})
	if readErr != nil {
		return errors.New("core_selection_unknown")
	}
	ack, restoreErr := client.SelectRoute(recovery, &pb.SelectRouteRequest{ExpectedTag: hello.SelectedTag, OutboundTag: tag})
	if restoreErr != nil || ack.SelectedTag != tag {
		return errors.New("core_selection_unknown")
	}
	s.lastProfile, s.lastRevision, s.selected, s.tags, s.applied = previous, revision, tag, tags, digest
	return err
}

// SwitchRoute returns committed=true even when old-connection cleanup is pending.
// Callers must publish the actual selection rather than report a false rollback.
func (s *Session) SwitchRoute(ctx context.Context, p config.RouteConfig) (bool, error) {
	if p.IPv4Only != s.lastIPv4Only {
		return false, errors.New("runtime_refresh_required")
	}
	if err := config.ValidateAddressFamily(p); err != nil {
		return false, err
	}
	target := config.RuntimeTag(p)
	if !s.tags[target] {
		return false, errors.New("runtime_refresh_required")
	}
	c, closer, state, err := s.open(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = closer.Close() }()
	if state != "STARTED" {
		return false, ErrUnavailable
	}
	ack, err := c.SelectRoute(ctx, &pb.SelectRouteRequest{OutboundTag: target, ExpectedTag: s.selected})
	if err != nil {
		recovery, done := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer done()
		hello, observeErr := c.Hello(recovery, &emptypb.Empty{})
		if observeErr != nil || hello.ServiceStatus != "STARTED" {
			return false, errors.New("core_selection_unknown")
		}
		if hello.SelectedTag == target {
			s.selected = target
			return true, errors.New("route_connections_pending")
		}
		if hello.SelectedTag != s.selected {
			return false, errors.New("core_selection_unknown")
		}
		return false, errors.New("route_selection_failed")
	}
	if ack.SelectedTag != target {
		return false, errors.New("route_selection_failed")
	}
	s.selected = target
	if !ack.OldConnectionsClosed {
		return true, errors.New("route_connections_pending")
	}
	return true, nil
}

// Power does not request authorization or start a missing/idle core.
func (s *Session) Power(ctx context.Context, sleeping bool, sequence uint64) error {
	c, closer, state, err := s.open(ctx)
	if errors.Is(err, ErrRequired) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = closer.Close() }()
	if state != "STARTED" {
		return nil
	}
	_, err = c.SetPaused(ctx, &pb.SetPausedRequest{Paused: sleeping, Sequence: sequence})
	if err != nil {
		return errors.New("core_power_failed")
	}
	return nil
}

type tunConflict struct{ params []string }

func (*tunConflict) Error() string                        { return "tun_conflict_other" }
func (e *tunConflict) ProductFailure() (string, []string) { return e.Error(), e.params }

// ReadUsage authenticates the root boundary and returns only product counters.
func (s *Session) ReadUsage(ctx context.Context) (config.UsageSnapshot, error) {
	c, closer, _, err := s.open(ctx)
	if err != nil {
		return config.UsageSnapshot{}, err
	}
	defer func() { _ = closer.Close() }()
	v, err := c.ReadUsage(ctx, &emptypb.Empty{})
	if err != nil {
		return config.UsageSnapshot{}, ErrUnavailable
	}
	out := config.UsageSnapshot{Session: v.Session, Sequence: v.Sequence, At: time.UnixMilli(v.SampledUnixMilli), Partial: v.Partial, ConnectionsKnown: v.ConnectionsKnown}
	for _, r := range v.Counters {
		out.Counters = append(out.Counters, config.UsageCounter{Tag: r.Tag, Upload: r.Upload, Download: r.Download, Connections: r.Connections})
	}
	return out, nil
}

// ObserveCore reads authenticated status without changing the runtime or adapter state.
func (s *Session) ObserveCore(ctx context.Context) (config.CoreObservation, error) {
	client, closer, _, err := s.open(ctx)
	if err != nil {
		return config.CoreObservation{}, err
	}
	defer func() { _ = closer.Close() }()
	hello, err := client.Hello(ctx, &emptypb.Empty{})
	if err != nil {
		return config.CoreObservation{}, ErrUnavailable
	}
	if hello.ProtocolVersion != 6 || hello.Fingerprint != s.fingerprint || int(hello.OwnerPid) != s.ownerPID {
		return config.CoreObservation{}, ErrIdentity
	}
	return config.CoreObservation{Status: hello.ServiceStatus, Interface: hello.Interface, SelectedTag: hello.SelectedTag, RuntimeSession: hello.RuntimeSession, RuntimeVersion: hello.RuntimeVersion}, nil
}

// MatchesAddressFamily reports the policy frozen into the currently applied profile.
func (s *Session) MatchesAddressFamily(ipv4Only bool) bool { return s.lastIPv4Only == ipv4Only }
