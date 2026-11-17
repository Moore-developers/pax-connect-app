package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"pax-connect/daemon/internal/mcpclientconfig"
	"pax-connect/daemon/internal/mcpinstall"
	"pax-connect/daemon/internal/storage"
)

type accessReport struct {
	Entry    string                   `json:"entry"`
	Clients  []mcpclientconfig.Client `json:"clients"`
	Verified bool                     `json:"verified"`
	Error    string                   `json:"error"`
}

func clientActions(args []string) {
	flags := flag.NewFlagSet("clients", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	if len(args) == 0 {
		fail("invalid_arguments")
	}
	action := args[0]
	bundlePath := flags.String("app-bundle", "", "current bundle")
	id := flags.String("client", "", "client configuration")
	allow := flags.String("allow", "read", "initial permissions")
	expected := flags.String("expected-state", "", "observed configuration state")
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 || *bundlePath == "" {
		fail("invalid_arguments")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	bundle, err := mcpinstall.Verify(ctx, *bundlePath)
	if err != nil {
		writeReport(accessReport{Entry: "repair", Clients: []mcpclientconfig.Client{}, Error: "installation_invalid"})
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fail("app_unavailable")
	}
	dir, err := mcpinstall.Directory()
	if err != nil {
		fail("app_unavailable")
	}
	launcher := filepath.Join(dir, "pax-mcp")
	manager := mcpclientconfig.New(home, launcher)
	report := accessReport{Entry: "ready", Clients: manager.Inspect()}
	installed, e := mcpinstall.Resolve(ctx)
	if os.IsNotExist(statError(launcher)) {
		report.Entry = "missing"
	} else if e != nil || installed != bundle || bundle.CheckLauncher(launcher) != nil {
		report.Entry = "repair"
	}
	switch action {
	case "inspect":
	case "connect":
		// Check configuration before repairing the entry; conflicts have no side effects.
		valid := false
		for _, c := range report.Clients {
			if c.ID == *id && (c.State == *expected || c.State == "configured") && (c.State == "missing" || c.State == "outdated" || c.State == "disabled" || c.State == "configured") {
				valid = true
			}
		}
		if !valid {
			report.Error = "configuration_changed"
			break
		}
		if report.Entry != "ready" {
			if _, e = mcpinstall.Install(ctx, bundle.Path, false); e != nil {
				report.Error = "installation_invalid"
				break
			}
			report.Entry = "ready"
		}
		if e = manager.Connect(ctx, *id, *allow, *expected); e != nil {
			report.Error = clientError(e)
		}
		report.Clients = manager.Inspect()
	case "repair":
		if _, e = mcpinstall.Install(ctx, bundle.Path, false); e != nil {
			report.Error = "installation_invalid"
		} else {
			report.Entry = "ready"
		}
	case "verify":
		if report.Entry != "ready" {
			report.Error = "installation_invalid"
			break
		}
		if e = verifyEntry(ctx, launcher); e != nil {
			report.Error = "entry_probe_failed"
		} else {
			report.Verified = true
		}
	default:
		report.Error = "invalid_arguments"
	}
	writeReport(report)
}
func statError(path string) error { _, err := os.Lstat(path); return err }
func writeReport(report accessReport) {
	if json.NewEncoder(os.Stdout).Encode(report) != nil {
		os.Exit(1)
	}
}
func clientError(err error) string {
	if errors.Is(err, storage.ErrCommitUncertain) {
		return "configuration_commit_uncertain"
	}
	switch err.Error() {
	case "configuration_changed", "configuration_conflict", "client_not_found", "configuration_backup_failed", "configuration_verify_failed", "invalid_permission":
		return err.Error()
	default:
		return "configuration_write_failed"
	}
}
func verifyEntry(ctx context.Context, launcher string) error {
	cmd := exec.CommandContext(ctx, launcher, "--client-id", "pax-entry-check", "--allow", "read")
	cmd.Stderr = io.Discard
	session, err := mcp.NewClient(&mcp.Implementation{Name: "pax-entry-check", Version: "1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: cmd, TerminateDuration: time.Second}, nil)
	if err != nil {
		return err
	}
	_, queryErr := session.ListTools(ctx, nil)
	closeErr := session.Close()
	if queryErr != nil {
		return queryErr
	}
	return closeErr
}
