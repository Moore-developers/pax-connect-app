package singbox

import (
	"context"
	"errors"
	"net"
	"sync"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	nativeOutbound "github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
)

// Runtime owns one lazy, unprivileged measurement Box. Each admitted task owns
// fresh native outbound/DNS modules in a cloned service registry; protocol pools
// and route-dependent DNS caches never survive the task or cross another route.
// Controller retains all product admission, budgets and frozen configuration.
type Runtime struct {
	exits    *measurement.ExitLookup
	mu       sync.Mutex
	instance *box.Box
	root     context.Context
	cancel   context.CancelFunc
	jobs     sync.WaitGroup
	closed   bool
	shutdown chan struct{}
	closeErr error
}

func NewMeasurements(lookup *measurement.ExitLookup) *Runtime { return &Runtime{exits: lookup} }

func (r *Runtime) acquire(ctx context.Context) (*box.Box, context.Context, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, nil, errors.New("measurement_runtime_closed")
	}
	if r.instance == nil {
		root, cancel := context.WithCancel(runtimeContext(context.Background()))
		var options option.Options
		if err := options.UnmarshalJSONContext(root, []byte(`{"log":{"disabled":true},"outbounds":[{"type":"direct","tag":"base"}],"route":{"final":"base","auto_detect_interface":true},"dns":{"servers":[{"type":"udp","tag":"base-dns","server":"223.5.5.5"}],"final":"base-dns"}}`)); err != nil {
			cancel()
			return nil, nil, ErrConfig
		}
		instance, err := box.New(box.Options{Context: root, Options: options})
		if err != nil {
			cancel()
			return nil, nil, measurementRuntimeError()
		}
		if err = instance.Start(); err != nil {
			cancel()
			_ = instance.Close()
			return nil, nil, measurementRuntimeError()
		}
		r.instance = instance
		r.root = root
		r.cancel = cancel
	}
	r.jobs.Add(1)
	return r.instance, r.root, nil
}
func measurementRuntimeError() error {
	return errors.Join(ErrProbe, &measurement.Failure{Code: "test_runtime_failed", Stage: "runtime_prepare"})
}

// Close cancels only measurement work and drains before retiring its shared Box.
func (r *Runtime) Close(ctx context.Context) error {
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		r.shutdown = make(chan struct{})
		if r.cancel != nil {
			r.cancel()
		}
		go func() {
			r.jobs.Wait()
			if r.instance != nil {
				r.closeErr = r.instance.Close()
			}
			close(r.shutdown)
		}()
	}
	done := r.shutdown
	r.mu.Unlock()
	select {
	case <-done:
		return r.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

type measurementNetwork struct {
	scope      *adapter.Scope
	outbounds  *nativeOutbound.Manager
	transports *dns.TransportManager
	resolver   *dns.Router
	once       sync.Once
}

func (n *measurementNetwork) close() {
	n.once.Do(func() {
		if n.scope != nil {
			_ = n.scope.Close()
		}

	})
}

func (r *Runtime) withMeasurementOutbound(ctx context.Context, p config.RouteConfig, use func(context.Context, adapter.Outbound) error) error {
	instance, root, err := r.acquire(ctx)
	if err != nil {
		return err
	}
	defer r.jobs.Done()
	// Preserve request values (including exit admission), but never mutate the
	// shared native service registry while registering task-owned components.
	task, cancel := context.WithCancel(service.ContextWithRegistry(ctx, service.RegistryFromContext(root).Clone()))
	defer cancel()
	stopRoot := context.AfterFunc(root, cancel)
	defer stopRoot()
	options, err := buildOptions(task, p, Rules{}, false, false)
	if err != nil {
		return err
	}
	factory := instance.LogFactory()
	logger := factory.NewLogger("measurement")
	network := &measurementNetwork{scope: adapter.NewScope(task, logger)}
	defer func() { cancel(); network.close() }()
	network.outbounds = nativeOutbound.NewManager(service.FromContext[adapter.OutboundRegistry](task), instance.Endpoint(), "proxy")
	service.MustRegister[adapter.OutboundManager](task, network.outbounds)
	network.transports = dns.NewTransportManager(service.FromContext[adapter.DNSTransportRegistry](task), network.outbounds, options.DNS.Final)
	service.MustRegister[adapter.DNSTransportManager](task, network.transports)
	network.resolver, err = dns.NewRouter(task, factory, *options.DNS)
	if err != nil {
		return measurementRuntimeError()
	}
	service.MustRegister[adapter.DNSRouter](task, network.resolver)
	for _, server := range options.DNS.Servers {
		if err = network.transports.Create(task, logger, server.Tag, server.Type, server.Options); err != nil {
			return measurementRuntimeError()
		}
	}
	if err = network.resolver.Initialize(options.DNS.Rules); err != nil {
		return measurementRuntimeError()
	}
	for _, out := range options.Outbounds {
		if err = network.outbounds.Create(task, instance.Router(), logger, out.Tag, out.Type, out.Options); err != nil {
			return measurementRuntimeError()
		}
	}
	for _, stage := range adapter.ListStartStages {
		for _, component := range []struct {
			name    string
			service adapter.Lifecycle
		}{
			{"measurement outbounds", network.outbounds}, {"measurement DNS transports", network.transports}, {"measurement DNS router", network.resolver},
		} {
			if err = network.scope.Start(component.name, component.service, stage); err != nil {
				return measurementRuntimeError()
			}
		}
	}
	if err = task.Err(); err != nil {
		return err
	}
	outbound, ok := network.outbounds.Outbound("proxy")
	if !ok {
		return ErrProbe
	}
	return use(task, &measurementOutbound{Outbound: outbound, lifetime: task})
}

// Every TCP connection belongs to its task even for stateless SOCKS/HTTP native
// outbounds that have no Close hook. Cancellation cannot leave a consumer alive.
type measurementOutbound struct {
	adapter.Outbound
	lifetime context.Context
}

func (o *measurementOutbound) DialContext(ctx context.Context, kind string, destination M.Socksaddr) (net.Conn, error) {
	bounded, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(o.lifetime, cancel)
	connection, err := o.Outbound.DialContext(bounded, kind, destination)
	if err != nil {
		stop()
		cancel()
		return nil, err
	}
	result := &measurementConn{Conn: connection, cancel: func() { stop(); cancel() }}
	result.stop = context.AfterFunc(o.lifetime, func() { _ = connection.Close() })
	// Native early-handshake streams (notably SS2022) publish request state on
	// their first Write. Finish that handshake before HTTP starts parallel
	// read/write loops; it remains part of this task's cold-request duration.
	if early, ok := connection.(interface{ NeedHandshake() bool }); ok && early.NeedHandshake() {
		closeOnDeadline := context.AfterFunc(bounded, func() { _ = connection.Close() })
		_, err = connection.Write(nil)
		closeOnDeadline()
		if bounded.Err() != nil {
			err = bounded.Err()
		}
		if err != nil {
			_ = result.Close()
			return nil, err
		}
	}
	if err = bounded.Err(); err != nil {
		_ = result.Close()
		return nil, err
	}

	return result, nil
}

type measurementConn struct {
	net.Conn
	stop   func() bool
	cancel context.CancelFunc
}

func (c *measurementConn) Close() error { c.stop(); c.cancel(); return c.Conn.Close() }
