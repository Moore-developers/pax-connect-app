package singbox

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/daemon"
	"google.golang.org/grpc"
)

type connectionSnapshot struct {
	grpc.ServerStream
	ctx    context.Context
	events *daemon.ConnectionEvents
}

func (s *connectionSnapshot) Context() context.Context { return s.ctx }
func (s *connectionSnapshot) Send(e *daemon.ConnectionEvents) error {
	s.events = e
	return errors.New("snapshot complete")
}

// SelectRoute changes the native selector and closes only the old node's flows.
// Lifecycle and this method are serialized by the root RPC server.
func (r *CoreRuntime) SelectRoute(ctx context.Context, expected, target string) (string, bool, error) {
	instance := r.current().Instance()
	if instance == nil || r.Status() != "STARTED" {
		return "", false, errors.New("core_not_started")
	}
	outbound, ok := instance.Box().Outbound().Outbound("proxy")
	selector, okGroup := outbound.(adapter.OutboundGroup)
	if !ok || !okGroup {
		return "", false, errors.New("core_selector_unavailable")
	}
	old := selector.Now()
	if !slices.Contains(selector.All(), target) {
		return old, false, errors.New("core_route_missing")
	}
	if old != target && old != expected {
		return old, false, errors.New("core_selection_stale")
	}
	if err := ctx.Err(); err != nil {
		return old, false, err
	}
	if old == target {
		err := r.closeRetired(ctx)
		return target, err == nil, err
	}
	if old != target {
		r.cancelCurrentPath(true)
		if _, err := r.current().SelectOutbound(ctx, &daemon.SelectOutboundRequest{GroupTag: "proxy", OutboundTag: target}); err != nil {
			return old, false, errors.New("core_selection_failed")
		}
		if r.retired == nil {
			r.retired = map[string]bool{}
		}
		r.retired[old] = true
	}
	delete(r.retired, target)
	// Answers depend on the selected egress; reset native connections and cache.
	instance.Box().Router().ResetNetwork()
	instance.Box().DNSRouter().ClearCache()
	err := r.closeRetired(ctx)
	return target, err == nil, err
}

func (r *CoreRuntime) closeRetired(ctx context.Context) error {
	if len(r.retired) == 0 || r.Status() != "STARTED" {
		return nil
	}
	connections, err := r.activeNativeConnections(ctx)
	if err != nil {
		return err
	}
	for _, c := range connections {
		if !r.retiredSelectorConnection(c) {
			continue
		}
		if _, err := r.current().CloseConnection(ctx, &daemon.CloseConnectionRequest{Id: c.Id}); err != nil {
			return errors.New("core_connections_pending")
		}
	}
	remaining, err := r.activeNativeConnections(ctx)
	if err != nil {
		return err
	}
	for _, c := range remaining {
		if r.retiredSelectorConnection(c) {
			return errors.New("core_connections_pending")
		}
	}
	return nil
}

func (r *CoreRuntime) retiredSelectorConnection(c *daemon.Connection) bool {
	if c == nil || c.ClosedAt != 0 || !slices.Contains(c.ChainList, "proxy") {
		return false
	}
	if r.retired[c.Outbound] {
		return true
	}
	for _, tag := range c.ChainList {
		if r.retired[tag] {
			return true
		}
	}
	return false
}

// Maintain retires late-arriving old-node flows as well as already tracked ones.
// Called by the same root owner as lifecycle RPCs; no parallel lifecycle writer.
func (r *CoreRuntime) Maintain(ctx context.Context) {
	_ = r.closeRetired(ctx)
	instance := r.current().Instance()
	if instance == nil || r.Status() != "STARTED" {
		r.physical = ""
		return
	}
	monitor := instance.Box().Network().InterfaceMonitor()
	if monitor != nil {
		current := monitor.DefaultInterface()
		if current == nil || strings.HasPrefix(current.Name, "utun") || current.Name == "lo0" {
			r.fixedHealth.invalidate(r.pathVersion.Load())
			return
		}
		r.DefaultInterfaceChanged(current.Name, current.Index)
	}
	// A gateway/Wi-Fi change can retain en0. Invalidate old verdicts before
	// consuming their completion, even when the interface name did not change.
	network := instance.Box().Network().NetworkEnvironment()
	if network != r.fixedNetwork {
		r.fixedHealth.invalidate(r.pathVersion.Load())
		r.fixedNetwork = network
	}
	r.maintainFixedRoutes(ctx)
}

// SetPaused forwards device state without constructing or reviving an instance.
func (r *CoreRuntime) SetPaused(paused bool) error {
	r.cancelCurrentPath(true)
	r.fixedPaused = paused
	instance := r.current().Instance()
	if instance == nil || r.Status() != "STARTED" {
		return nil
	}
	manager := instance.PauseManager()
	if manager == nil {
		return errors.New("core_pause_unavailable")
	}
	if paused {
		manager.DevicePause()
	} else {
		manager.DeviceWake()
		_, err := r.current().CloseAllConnections(context.Background(), nil)
		return err
	}
	return nil
}

// NetworkChanged closes existing connections after a physical path change.
func (r *CoreRuntime) NetworkChanged() {
	r.cancelCurrentPath(true)
	if r.Status() == "STARTED" {
		_, _ = r.current().CloseAllConnections(context.Background(), nil)
	}
}

// SelectedTag reads the actual upstream group, not persisted selector cache.
func (r *CoreRuntime) SelectedTag() string {
	instance := r.current().Instance()
	if instance == nil {
		return ""
	}
	out, ok := instance.Box().Outbound().Outbound("proxy")
	if !ok {
		return ""
	}
	if group, ok := out.(adapter.OutboundGroup); ok {
		return group.Now()
	}
	return ""
}

// DefaultInterfaceChanged accepts physical-interface facts from the native
// monitor. TUN/self events and unavailable samples do not replace its baseline.
func (r *CoreRuntime) DefaultInterfaceChanged(name string, index int) {
	if name == "" || index <= 0 || strings.HasPrefix(name, "utun") || name == "lo0" {
		return
	}
	path := fmt.Sprintf("%s:%d", name, index)
	if r.physical != "" && r.physical != path {
		r.NetworkChanged()
	}
	r.physical = path
}
