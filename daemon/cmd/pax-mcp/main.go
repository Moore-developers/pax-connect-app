// pax-mcp is a stdio adapter. It never launches the App, daemon or root core.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"pax-connect/daemon/internal/mcpserver"
)

func main() {
	socket := flag.String("socket", "", "explicit development socket (same-UID peer required)")
	bundle := flag.String("app-bundle", "", "verified App bundle; defaults to /Applications/PaxConnect.app")
	expectedHash := flag.String("expected-bundle-code-hash", "", "launcher-pinned bundle identity; reconnect after an App upgrade")
	client := flag.String("client-id", "pax-local", "stable local client identity")
	allow := flag.String("allow", "read", "comma-separated permissions; default read")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "pax-mcp: invalid_arguments")
		os.Exit(2)
	}
	server, err := mcpserver.New(mcpserver.Options{SocketPath: *socket, AppBundle: *bundle, BundleCodeHash: *expectedHash, ClientID: *client, Allow: *allow})
	if err != nil {
		fmt.Fprintln(os.Stderr, "pax-mcp: invalid_configuration")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err = server.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "pax-mcp: transport_closed")
		os.Exit(1)
	}
}
