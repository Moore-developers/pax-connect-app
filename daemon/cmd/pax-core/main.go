package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"pax-connect/daemon/internal/core"
	"pax-connect/daemon/internal/singbox"
	"syscall"
)

var version = "dev"

func main() {
	var o core.Options
	showKernel := flag.Bool("kernel-version", false, "print embedded sing-box version")
	flag.IntVar(&o.OwnerPID, "owner-pid", 0, "owner process PID")
	flag.IntVar(&o.OwnerUID, "owner-uid", 0, "owner user UID")
	flag.StringVar(&o.Socket, "socket", "/private/var/run/pax-core/core.sock", "private core Unix socket")
	flag.StringVar(&o.Resources, "resources", "", "bundle rule-set directory")
	flag.StringVar(&o.State, "state", "/private/var/run/pax-core/state", "root-owned private state directory")
	flag.IntVar(&o.MixedPort, "mixed-port", 0, "Pax loopback mixed port")
	flag.BoolVar(&o.Replace, "replace", false, "replace only this App's identity-recorded core")
	flag.Parse()
	if *showKernel {
		fmt.Println(singbox.PinnedModuleVersion)
		return
	}
	o.Version = version
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	// Runtime context outlives cancellation so cleanup can finish.
	r := singbox.NewCoreRuntime(context.Background())
	if err := core.Run(ctx, o, r, singbox.ValidateCoreOptions); err != nil {
		fmt.Fprintln(os.Stderr, "pax-core:", err)
		os.Exit(1)
	}
}
