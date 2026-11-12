package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/grpc"
	pb "pax-connect/daemon/gen/proto/corev1"
	"pax-connect/daemon/internal/network"
	"pax-connect/daemon/internal/processidentity"
)

type host struct {
	conflict  func(context.Context, string) network.ConflictResult
	lease     func(context.Context, Options, processidentity.Identity) (func(), error)
	euid      func() int
	prepare   func(Options) error
	copyRules func(Options) error
	listen    func(Options) (net.Listener, error)
	cleanup   func(context.Context, int) error
	release   func(context.Context, int) error
	readOwner func(int) (processidentity.Identity, error)
	alive     func(processidentity.Identity) bool
	ticks     <-chan time.Time
}

func nativeHost() host {
	return host{conflict: network.AssessOwnedConflict, lease: coreLease, euid: os.Geteuid, prepare: preparePaths, copyRules: copyRuleSets, listen: listenSocket, cleanup: func(ctx context.Context, port int) error { return cleanupProxy(ctx, port, runCommand) }, release: func(ctx context.Context, port int) error { return releaseProxy(ctx, port, runCommand) }, readOwner: processidentity.Read, alive: processidentity.Alive}
}

// Run serves until the owner exits, a signal cancels ctx, or Shutdown succeeds.
// It never installs a service. Tests replace privileged OS seams in run.
func Run(ctx context.Context, o Options, r Runtime, parse ProfileValidator) error {
	return run(ctx, o, r, parse, nativeHost())
}
func run(ctx context.Context, o Options, r Runtime, parse ProfileValidator, h host) error {
	defer r.Close()
	if h.euid() != 0 {
		return errors.New("core_root_required")
	}
	if o.OwnerPID <= 1 || o.OwnerUID <= 0 || o.MixedPort <= 0 || o.MixedPort > 65535 || !filepath.IsAbs(o.Socket) || !filepath.IsAbs(o.Resources) || !filepath.IsAbs(o.State) || filepath.Clean(o.State) == "/" || filepath.Dir(filepath.Clean(o.Socket)) == "/" || filepath.Clean(o.State) == filepath.Dir(filepath.Clean(o.Socket)) {
		return errors.New("core_flags_invalid")
	}
	owner, err := h.readOwner(o.OwnerPID)
	if err != nil || int(owner.UID) != o.OwnerUID {
		return errors.New("core_owner_invalid")
	}
	if err = h.prepare(o); err != nil {
		return err
	}
	if h.lease != nil {
		release, err := h.lease(ctx, o, owner)
		if err != nil {
			return err
		}
		defer release()
	}
	s, err := newServer(o, r, parse)
	if err != nil {
		return err
	}
	s.conflict = h.conflict
	if h.release != nil {
		s.releaseProxy = func(ctx context.Context) error { return h.release(ctx, o.MixedPort) }
	}
	// Bind first to reject an already-running core before any cleanup mutation.
	listener, err := h.listen(o)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	if err = h.copyRules(o); err != nil {
		return err
	}
	if err = h.cleanup(ctx, o.MixedPort); err != nil {
		return err
	}
	g := grpc.NewServer(grpc.MaxRecvMsgSize(maxProfileBytes+(1<<20)+1024), grpc.UnaryInterceptor(unaryAllow), grpc.StreamInterceptor(streamAllow))
	pb.RegisterPaxCoreServer(g, s)
	r.Register(g)
	slog.Info("core_ready", "operation_id", "startup", "snapshot_version", 0, "core_version", o.Version, "protocol_version", 1, "owner_pid", o.OwnerPID, "socket_mode", "0600")
	served := make(chan error, 1)
	go func() { served <- g.Serve(listener) }()
	ticks := h.ticks
	if ticks == nil {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		ticks = ticker.C
	}
	err = waitOwner(ctx, s.done, ticks, func() bool {
		s.mu.Lock()
		if runtime, ok := r.(interface{ Maintain(context.Context) }); ok {
			runtime.Maintain(ctx)
		}
		s.mu.Unlock()
		return h.alive(owner)
	}, served)
	clearErr := s.stop()
	// Drain the Shutdown response, but do not let open subscriptions hold exit.
	drained := make(chan struct{})
	go func() { g.GracefulStop(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(time.Second):
		g.Stop()
		<-drained
	}
	// Closing the Unix listener removes only the socket inode it created.
	if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return err
	}
	if clearErr != nil {
		slog.Error("core_clear_failed", "operation_id", "shutdown", "snapshot_version", 0, "reason", "clear_failed_after_retry")
		return errors.New("core_clear_failed")
	}
	return nil
}
func waitOwner(ctx context.Context, shutdown <-chan struct{}, ticks <-chan time.Time, alive func() bool, served <-chan error) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-shutdown:
			return nil
		case err := <-served:
			return err
		case <-ticks:
			if !alive() {
				return nil
			}
		}
	}
}
func preparePaths(o Options) error {
	if err := protectedDirectory(filepath.Dir(o.Socket), 0755); err != nil {
		return err
	}
	if err := protectedDirectory(o.State, 0700); err != nil {
		return fmt.Errorf("core state: %w", err)
	}
	// Refuse pre-existing cache links/non-files before bbolt can follow them.
	if info, err := os.Lstat(filepath.Join(o.State, "cache.db")); err == nil {
		if !info.Mode().IsRegular() || !rootOwned(info) {
			return errors.New("core_cache_unsafe")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
