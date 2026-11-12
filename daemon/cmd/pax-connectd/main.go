// Command pax-connectd assembles the user-owned product daemon. Only pax-core
// owns privileged networking; legacy root/session launch modes are removed.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/cfoptimizer"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/controller"
	"pax-connect/daemon/internal/ipc"
	"pax-connect/daemon/internal/measurement"
	"pax-connect/daemon/internal/network"
	"pax-connect/daemon/internal/singbox"
	"pax-connect/daemon/internal/siteicon"
	"pax-connect/daemon/internal/storage"
	"pax-connect/daemon/internal/subfetch"
	"pax-connect/daemon/internal/telemetry"
	"runtime"
	"syscall"
	"time"
)

var version = "0.1.0-dev"

func main() {
	userMode := flag.Bool("user", false, "run unprivileged with private user stores")
	ownerPID := flag.Int("owner-pid", 0, "App process identity to watch")
	printSocket := flag.Bool("print-user-socket", false, "print canonical user socket")
	checkCore := flag.Bool("check-core", false, "check bundled core identity")
	shutdownCore := flag.Bool("shutdown-core", false, "drain this App's core")
	mixed := flag.String("mixed-address", singbox.DefaultMixedAddress, "Pax loopback HTTP/SOCKS endpoint")
	showVersion := flag.Bool("version", false, "print build version")
	showKernel := flag.Bool("kernel-version", false, "print embedded sing-box version")
	socket := flag.String("socket", "", "private user Unix socket")
	level := flag.String("log-level", "info", "debug|info|warn|error")
	flag.Parse()
	if *showKernel {
		fmt.Println(singbox.PinnedModuleVersion)
		return
	}
	if *showVersion {
		fmt.Println(version)
		return
	}
	if *printSocket {
		path, err := userSocketPath()
		if err != nil {
			fmt.Fprintln(os.Stderr, "user_socket_unavailable")
			os.Exit(1)
		}
		fmt.Println(path)
		return
	}
	if os.Geteuid() == 0 {
		fmt.Fprintln(os.Stderr, "user_daemon_root_forbidden")
		os.Exit(1)
	}
	if *checkCore || *shutdownCore {
		ctx, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		core, err := coreSession(*ownerPID, singbox.Rules{}, *mixed)
		if err == nil {
			if *shutdownCore {
				err = core.Shutdown(ctx)
			} else {
				err = core.Ready(ctx)
			}
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "core_not_ready")
			os.Exit(1)
		}
		return
	}
	if !*userMode {
		fmt.Fprintln(os.Stderr, "user_mode_required")
		os.Exit(1)
	}
	if *socket == "" {
		var err error
		*socket, err = userSocketPath()
		if err != nil {
			fmt.Fprintln(os.Stderr, "user_socket_unavailable")
			os.Exit(1)
		}
	}
	logger, closeLog := operationalLogger(*level)
	defer closeLog()
	slog.SetDefault(logger)
	if err := run(*socket, logger, *mixed, *ownerPID); err != nil {
		logger.Error("daemon stopped", "reason", "user_daemon_failed")
		os.Exit(1)
	}
}

func run(socketPath string, logger *slog.Logger, mixed string, ownerPID int) (result error) {
	lifetime, finish := context.WithCancel(context.Background())
	defer finish()
	directory, err := userDataDirectory()
	if err != nil {
		return err
	}
	initialInstall, err := initialInstallation(directory)
	if err != nil {
		return err
	}
	if err = storage.PreparePrivateDirectory(directory, uint32(os.Geteuid())); err != nil {
		return err
	}
	telemetry.ConfigureDiagnostics(filepath.Join(directory, "diagnostics.jsonl"))
	if ownerPID != 0 {
		join, err := watchUserOwner(lifetime, finish, ownerPID)
		if err != nil {
			return err
		}
		defer join()
	}
	listener, err := ipc.ListenUser(socketPath)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	fingerprint, err := serviceFingerprint()
	if err != nil {
		return err
	}
	logger.Info("pax-connectd starting", "operation_id", "startup", "snapshot_version", 0, "version", version, "sing_box_version", singbox.PinnedModuleVersion, "protocol_version", controller.ProtocolVersion, "socket_owner", socketOwner(listener), "euid", os.Geteuid())
	ctx, stop := signal.NotifyContext(lifetime, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	state := controller.New(version, fingerprint)
	state.ConfigureInitialInstall(initialInstall)
	state.ConfigureUserMode(func() { finish(); stop() }, uint32(ownerPID))
	owner := uint32(os.Geteuid())
	state.ConfigureCatalog(config.NewCatalogStore(filepath.Join(directory, "routes.json"), owner))
	state.ConfigureRoutingLibrary(config.NewRoutingLibraryStore(filepath.Join(directory, "routing-library.json"), owner), singbox.DecodeRoutingSource)
	state.ConfigureMeasurements(config.NewMeasurementStore(filepath.Join(directory, "route-measurements.json"), owner))
	state.ConfigureTLS(config.NewTLSPolicyStore(filepath.Join(directory, "tls-policies.json"), owner))
	state.ConfigureNetworkPrivacy(config.NewNetworkPrivacyStore(filepath.Join(directory, "network-privacy.json"), owner))
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	resources := filepath.Join(filepath.Dir(filepath.Dir(executable)), "Resources", "rulesets")
	if err := state.ConfigureGeoRules(resources); err != nil {
		return errors.New("geo_baseline_invalid")
	}
	if err := state.ConfigureAdblockRules(ctx, resources); err != nil {
		return fmt.Errorf("adblock_baseline_invalid: %w", err)
	}
	rules := singbox.Rules{Directory: resources, GoogleHash: "992d7ba91ce9c6e1c248d9ef0d58b2a17281415526b6a560dce6368e4f72e1f2", GeositeHash: "2f4af0be08f2ff494a8fcdef992dc92a2267600ce26c9bba17421d1ce9a6715c", GeoIPHash: "ebee603fdf402314b44b9f653cdcf6d9cc9c41e84e2b3515e3123c5c920a93bc"}
	core, err := coreSession(ownerPID, rules, mixed)
	if err != nil {
		return errors.New("core_bundle_invalid")
	}
	state.ConfigureCoreSession(core)
	state.ConfigureRouteData(directory, owner)
	storageStarted := time.Now()
	if err := state.ConfigureHistory(context.WithoutCancel(ctx), directory, owner); err != nil {
		logger.Warn("history unavailable", "operation_id", "startup", "snapshot_version", 0, "reason", "history_store_failed", "library", "history", "elapsed_ms", time.Since(storageStarted).Milliseconds())
	}
	defer func() {
		closeCtx, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		if err := state.CloseHistory(closeCtx); result == nil {
			result = err
		}
	}()
	if err := state.ConfigureHistoryConsumers(controller.HistoryConsumerLogs, controller.HistoryConsumerTraffic); err != nil {
		return err
	}
	storageStarted = time.Now()
	if err := state.ConfigureHistoryLogs(context.WithoutCancel(ctx), directory, owner); err != nil {
		logger.Warn("history logs unavailable", "operation_id", "startup", "snapshot_version", 0, "reason", "history_logs_unavailable", "library", "logs", "elapsed_ms", time.Since(storageStarted).Milliseconds())
	}
	defer func() {
		closeCtx, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		if err := state.CloseHistoryLogs(closeCtx); result == nil {
			result = err
		}
	}()
	storageStarted = time.Now()
	if err := state.ConfigureHistoryTraffic(context.WithoutCancel(ctx), directory, owner); err != nil {
		logger.Warn("history traffic unavailable", "operation_id", "startup", "snapshot_version", 0, "reason", "history_traffic_unavailable", "library", "traffic", "elapsed_ms", time.Since(storageStarted).Milliseconds())
	}
	defer func() {
		closeCtx, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		if err := state.CloseHistoryTraffic(closeCtx); result == nil {
			result = err
		}
	}()
	storageStarted = time.Now()
	if err := state.ConfigureHistoryIngest(context.WithoutCancel(ctx), directory, owner); err != nil {
		logger.Warn("history ingest unavailable", "operation_id", "startup", "snapshot_version", 0, "reason", "history_journal_unavailable", "library", "journal", "elapsed_ms", time.Since(storageStarted).Milliseconds())
	}
	defer func() {
		closeCtx, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		if err := state.CloseHistoryIngest(closeCtx); result == nil {
			result = err
		}
	}()
	state.ConfigureOperations(ctx, func() (config.DebugProfile, error) { return config.DebugProfile{}, config.ErrProfileUnavailable })
	state.ConfigureNetworkEpoch(network.Epoch)
	state.ConfigurePathDiagnostics(network.NewPathInspector().Inspect)
	exits := measurement.NewExitLookup()
	kernel := singbox.NewMeasurements(exits)
	state.ConfigureExitLookup(exits)
	defer func() {
		closeCtx, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		if err := kernel.Close(closeCtx); result == nil {
			result = err
		}
	}()
	memory, capacityErr := unix.SysctlUint64("hw.memsize")
	capacity := measurement.Recommended(runtime.NumCPU(), memory)
	state.ConfigureRouteTester(kernel, capacity.Ceiling)
	state.ConfigureMeasurementPreferences(config.NewMeasurementPreferenceStore(filepath.Join(directory, "measurement-preferences.json"), owner), capacity.Ceiling)
	state.ConfigurePhysicalEgressCache(config.NewPhysicalEgressCacheStore(filepath.Join(directory, "physical-egress-cache.json"), owner))
	state.ConfigurePhysicalEgress(network.PhysicalNetwork, controller.NewPhysicalEgressProbe(exits), controller.WithRouteWatch(network.StartRouteWatch), controller.WithExternalTUN(network.AssessOwnedConflict))
	state.ConfigureCFCandidates()
	state.ConfigureCFWorkspace(storage.NewPrivateFile(filepath.Join(directory, "cf-evidence.json"), owner, 4*1024*1024), func(p network.Physical) cfoptimizer.Probe { return cfPhysicalProbe(p, exits) })
	logger.Info("measurement admission budget", "operation_id", "startup", "snapshot_version", 0, "initial", capacity.Initial, "recommended_ceiling", capacity.Ceiling, "memory_known", capacityErr == nil)
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if err := state.CloseProxy(shutdown); result == nil {
			result = err
		}
	}()
	state.ConfigureSubscriptions(subfetch.New())
	state.ConfigureSubscriptionRequestFetch(func(ctx context.Context, p config.RouteConfig, url, agent string) ([]byte, subfetch.Report, error) {
		var body []byte
		var report subfetch.Report
		err := kernel.WithRouteNetwork(ctx, p, func(dial func(context.Context, string, string) (net.Conn, error), lookup func(context.Context, string) ([]netip.Addr, error)) error {
			var err error
			body, report, err = subfetch.NewWithOptions(subfetch.Options{DialContext: dial, Lookup: lookup}).FetchSubscriptionRequest(ctx, url, agent)
			return err
		})
		return body, report, err
	})

	cacheRoot, cacheErr := os.UserCacheDir()
	if cacheErr == nil {
		iconStore, iconErr := siteicon.New(filepath.Join(cacheRoot, "Pax Connect", "ObjectIcons", "v1"), owner, func(iconCtx context.Context, host string) ([]byte, error) {
			p, e := state.IconRoute(iconCtx)
			if e != nil {
				return nil, e
			}
			if p == nil {
				return subfetch.New().FetchIcon(iconCtx, host)
			}
			var body []byte
			e = kernel.WithRouteNetwork(iconCtx, *p, func(dial func(context.Context, string, string) (net.Conn, error), lookup func(context.Context, string) ([]netip.Addr, error)) error {
				var err error
				body, err = subfetch.NewWithOptions(subfetch.Options{DialContext: dial, Lookup: lookup}).FetchIcon(iconCtx, host)
				return err
			})
			return body, e
		})
		if iconErr == nil {
			state.ConfigureObjectIcons(iconStore)
			defer iconStore.Close()
		}
	}
	server := grpc.NewServer()
	paxv1.RegisterPaxServiceServer(server, state)
	if profiles, err := userAIProfiles(directory); err == nil {
		defer func() { _ = profiles.Close() }()
		state.ConfigureAIProfiles()
		paxv1.RegisterAIProfilesServiceServer(server, controller.NewAIProfilesServer(profiles))
		if usage, e := userAIUsage(ctx, directory, profiles, server); e == nil {
			state.ConfigureAIUsage(ctx, usage)
			defer func() {
				drain, cancel := context.WithTimeout(context.Background(), 4*time.Second)
				defer cancel()
				_ = usage.Close(drain)
			}()
		} else {
			logger.Warn("AI usage unavailable", "library", "ai_usage", "code", "ai_usage_unavailable")
		}
	} else {
		logger.Warn("AI Profile storage unavailable", "operation_id", "ai-profiles-startup", "snapshot_version", 0, "reason", "ai_store_invalid")
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	select {
	case err := <-served:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			return err
		}
	case <-ctx.Done():
		drain, done := context.WithTimeout(context.Background(), 4*time.Second)
		err := state.CloseProxy(drain)
		done()
		if err != nil {
			server.Stop()
			return err
		}
		stopServer(server, time.Second)
	}
	return nil
}
func newLogger(level string) *slog.Logger {
	var parsed slog.Level
	switch level {
	case "debug":
		parsed = slog.LevelDebug
	case "warn":
		parsed = slog.LevelWarn
	case "error":
		parsed = slog.LevelError
	default:
		parsed = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: parsed}))
}

// socketOwner 返回 socket 文件的实际属主/权限描述，用于确认 root:admin 0660 是否生效。
func socketOwner(listener net.Listener) string {
	address, ok := listener.Addr().(*net.UnixAddr)
	if !ok {
		return "unknown"
	}
	info, err := os.Stat(address.Name)
	if err != nil {
		return "stat failed"
	}
	stat := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("uid=%d gid=%d mode=%04o", stat.Uid, stat.Gid, info.Mode().Perm())
}

// stopServer bounds draining so a long-lived Watch cannot block daemon shutdown.
func stopServer(server *grpc.Server, timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(done)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		server.Stop()
		<-done
	}
}

// serviceFingerprint proves the actually running executable, not its marketing version.
func serviceFingerprint() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		return "", err
	}
	plist := filepath.Join(filepath.Dir(filepath.Dir(executable)), "Library", "LaunchDaemons", "dev.moore.pax-connect.daemon.plist")
	data, err := os.ReadFile(plist)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write(binary)
	_, _ = hash.Write(data)
	return hex.EncodeToString(hash.Sum(nil)), nil
}
