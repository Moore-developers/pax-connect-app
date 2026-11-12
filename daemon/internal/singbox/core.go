package singbox

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/sagernet/sing-box/daemon"
	"github.com/sagernet/sing-box/experimental/paxobs"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/json"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

// CoreRuntime adapts StartedService without exposing its upstream types.
// Its caller serializes lifecycle/status calls; upstream owns subscription locks.
type CoreRuntime struct {
	routingProfile string
	pathMu         sync.Mutex
	pathVersion    atomic.Uint64
	pathLease      *currentPathLease
	mixedAddress   string
	pathDetours    map[string]string
	pathTarget     string
	pathTLS        *tls.Config
	observations   *coreObservations
	physical       string
	mu             sync.RWMutex
	svc            *daemon.StartedService
	ctx            context.Context
	handler        *corePlatform
	retired        map[string]bool
	iface          string
}

// NewCoreRuntime builds an idle service. It creates no listeners or TUN.
func NewCoreRuntime(ctx context.Context) *CoreRuntime {
	ctx = runtimeContext(ctx)
	h := &corePlatform{}
	r := &CoreRuntime{observations: newCoreObservations(), ctx: ctx, handler: h}
	ctx = paxobs.WithRuleNames(ctx)
	ctx = paxobs.WithSink(ctx, r.observations.capture)
	r.ctx = ctx
	r.svc = daemon.NewStartedService(daemon.ServiceOptions{Context: ctx, Handler: h, Debug: true, LogMaxLines: 256})
	return r
}

// Register exposes upstream RPCs; core must install its allowlist interceptors.
func (r *CoreRuntime) Register(s grpc.ServiceRegistrar) {
	daemon.RegisterStartedServiceServer(coreRegistrar{s, r}, r.current())
}

// Apply replaces the instance using upstream reload semantics.
func (r *CoreRuntime) Apply(profile string) error {
	r.cancelCurrentPath(true)
	r.observations.collecting.Store(false)
	defer func() { r.observations.collecting.Store(r.Status() == "STARTED") }()
	var opts option.Options
	if err := opts.UnmarshalJSONContext(r.ctx, []byte(profile)); err != nil {
		return err
	}
	r.pathDetours = map[string]string{}
	for _, out := range opts.Outbounds {
		if dialer, ok := out.Options.(option.DialerOptionsWrapper); ok {
			r.pathDetours[out.Tag] = dialer.TakeDialerOptions().Detour
		}
	}
	iface := ""
	r.mixedAddress = ""
	for _, in := range opts.Inbounds {
		if mixed, ok := in.Options.(*option.HTTPMixedInboundOptions); ok && in.Type == "mixed" {
			r.mixedAddress = net.JoinHostPort("127.0.0.1", strconv.Itoa(int(mixed.ListenPort)))
		}
		if tunOptions, ok := in.Options.(*option.TunInboundOptions); ok {
			if !routeOwnershipGuard {
				return errors.New("core_route_guard_required")
			}
			iface = tun.CalculateInterfaceName("")
			tunOptions.InterfaceName = iface
		}
	}
	raw, err := json.MarshalContext(r.ctx, opts)
	if err != nil {
		return err
	}
	r.retired = nil
	r.observations.mu.Lock()
	r.observations.epoch++
	r.observations.mu.Unlock()
	r.iface = iface
	if err := r.current().StartOrReloadService(r.ctx, string(raw), nil); err != nil {
		return err
	}
	r.routingProfile = profile
	for _, out := range opts.Outbounds {
		if selector, ok := out.Options.(*option.SelectorOutboundOptions); ok && out.Tag == "proxy" && selector.Default != "" {
			_, _, err := r.SelectRoute(context.Background(), r.SelectedTag(), selector.Default)
			return err
		}
	}
	return nil
}

// Clear closes an instance, including one left in FATAL by a failed start.
func (r *CoreRuntime) Clear() error {
	r.routingProfile = ""
	r.cancelCurrentPath(true)
	r.observations.collecting.Store(false)
	r.retired = nil
	current := r.current()
	// Upstream CloseService rejects FATAL, even when it still owns an instance.
	// Retire that service and its subscriptions only after closing its instance.
	if r.Status() == "FATAL" {
		if instance := current.Instance(); instance != nil {
			if err := instance.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
				return err
			}
		}
		next := daemon.NewStartedService(daemon.ServiceOptions{Context: r.ctx, Handler: r.handler, Debug: true, LogMaxLines: 256})
		r.mu.Lock()
		r.svc = next
		r.mu.Unlock()
		current.Close()
		r.iface = ""
		return nil
	}
	if current.Instance() == nil {
		return nil
	}
	err := current.CloseService()
	r.observations.seal()
	if err == nil {
		r.iface = ""
	}
	return err
}

// coreRegistrar keeps the registered RPC service stable when a failed instance
// requires retiring StartedService. New RPCs resolve the current service;
// subscriptions on the retired service finish through upstream observer Close.
type coreRegistrar struct {
	grpc.ServiceRegistrar
	runtime *CoreRuntime
}

func (r coreRegistrar) RegisterService(desc *grpc.ServiceDesc, impl any) {
	copyDesc := *desc
	copyDesc.Methods = append([]grpc.MethodDesc(nil), desc.Methods...)
	copyDesc.Streams = append([]grpc.StreamDesc(nil), desc.Streams...)
	for i, m := range copyDesc.Methods {
		handler := m.Handler
		copyDesc.Methods[i].Handler = func(_ any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
			return handler(r.runtime.current(), ctx, decode, interceptor)
		}
	}
	for i, m := range copyDesc.Streams {
		handler := m.Handler
		copyDesc.Streams[i].Handler = func(_ any, stream grpc.ServerStream) error { return handler(r.runtime.current(), stream) }
	}
	r.ServiceRegistrar.RegisterService(&copyDesc, impl)
}
func (r *CoreRuntime) current() *daemon.StartedService {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.svc
}

// Close releases service observers after all RPCs have drained.
func (r *CoreRuntime) Close() { r.cancelCurrentPath(true); r.current().Close() }

// Status reads the upstream status using its first subscription value.
func (r *CoreRuntime) Status() string {
	s := &coreStatusStream{}
	_ = r.current().SubscribeServiceStatus(&emptypb.Empty{}, s)
	return s.value
}

type coreStatusStream struct {
	grpc.ServerStream
	value string
}

func (*coreStatusStream) Context() context.Context { return context.Background() }
func (s *coreStatusStream) Send(v *daemon.ServiceStatus) error {
	s.value = v.Status.String()
	return errors.New("snapshot complete")
}

var errCoreUnsupported = errors.New("core_platform_unsupported")

type corePlatform struct {
	mu    sync.Mutex
	lines [256]string
	next  int
}

func (*corePlatform) ServiceStop() error   { return errCoreUnsupported }
func (*corePlatform) ServiceReload() error { return errCoreUnsupported }
func (*corePlatform) SystemProxyStatus() (*daemon.SystemProxyStatus, error) {
	return nil, errCoreUnsupported
}
func (*corePlatform) SetSystemProxyEnabled(bool) error { return errCoreUnsupported }
func (*corePlatform) TriggerNativeCrash() error        { return errCoreUnsupported }
func (*corePlatform) ConnectSSHAgent() (int32, error)  { return 0, errCoreUnsupported }
func (p *corePlatform) WriteDebugMessage(m string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(m) > 4096 {
		m = m[:4096]
	}
	p.lines[p.next] = m
	p.next = (p.next + 1) % len(p.lines)
}

// RecordFailure records a caller-scrubbed reason in the bounded service log ring.
func (r *CoreRuntime) RecordFailure(detail string) {
	if len(detail) > 256 {
		detail = detail[:256]
	}
	r.current().WriteMessage(log.LevelError, detail)
}

// Interface reports only the name this instance was told to create.
func (r *CoreRuntime) Interface() string { return r.iface }
