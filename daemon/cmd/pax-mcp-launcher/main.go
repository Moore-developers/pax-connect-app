// pax-mcp-launcher installs or execs the verified registered App's MCP adapter.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"pax-connect/daemon/internal/mcpinstall"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--clients" {
		clientActions(os.Args[2:])
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if len(os.Args) > 1 && (os.Args[1] == "--install" || os.Args[1] == "--refresh") {
		flags := flag.NewFlagSet("install", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		bundle := flags.String("app-bundle", "", "current App bundle")
		if flags.Parse(os.Args[2:]) != nil || flags.NArg() != 0 || *bundle == "" {
			fail("invalid_arguments")
		}
		target, err := mcpinstall.Install(ctx, *bundle, os.Args[1] == "--refresh")
		if err != nil {
			fail("installation_invalid: open Pax Connect and install/repair the MCP entry")
		}
		if target != "" {
			_, _ = fmt.Fprintln(os.Stdout, target)
		}
		return
	}
	// Production clients can only choose identity and permissions.
	flags := flag.NewFlagSet("pax-mcp", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	client := flags.String("client-id", "pax-local", "client identity")
	allow := flags.String("allow", "read", "permissions")
	if flags.Parse(os.Args[1:]) != nil || flags.NArg() != 0 {
		fail("invalid_arguments")
	}
	bundle, err := mcpinstall.Resolve(ctx)
	if err != nil {
		fail("app_unavailable: open Pax Connect and install/repair the MCP entry")
	}
	own, err := os.Executable()
	if err != nil || bundle.CheckLauncher(own) != nil {
		fail("app_mismatch: open Pax Connect and install/repair the MCP entry")
	}
	binary := filepath.Join(bundle.Path, "Contents/MacOS/pax-mcp")
	args := []string{binary, "--app-bundle", bundle.Path, "--expected-bundle-code-hash", bundle.CodeHash, "--client-id", *client, "--allow", *allow}
	if syscall.Exec(binary, args, os.Environ()) != nil {
		fail("app_unavailable: open Pax Connect and install/repair the MCP entry")
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "pax-mcp:", message)
	os.Exit(1)
}
