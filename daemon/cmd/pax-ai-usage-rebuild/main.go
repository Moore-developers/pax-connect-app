// Command pax-ai-usage-rebuild recomputes ai_usage.db under the current usage rules:
// one pipeline, every request attributed by the profile switch timeline and priced.
// It is the only way to reprocess history; Open only adds compatible tables to v5/v6.
//
//	make ai-usage-rebuild                  # rebuild and replace (after a timestamped backup)
//	make ai-usage-rebuild ARGS=-dry-run    # build and report, replace nothing
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"pax-connect/daemon/internal/aiprofile"
	"pax-connect/daemon/internal/aiusage"
)

// The history before the profile switch timeline began: which Profile each CLI used.
var seedProfiles = []struct {
	cli  aiprofile.CLI
	name string
}{{aiprofile.Codex, "Krill"}, {aiprofile.Claude, "Derouter.ai"}}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, out, errw io.Writer) int {
	home, _ := os.UserHomeDir()
	flags := flag.NewFlagSet("pax-ai-usage-rebuild", flag.ContinueOnError)
	flags.SetOutput(errw)
	dir := flags.String("dir", filepath.Join(home, "Library", "Application Support", "Pax Connect"), "Pax Connect data directory")
	logs := flags.String("home", home, "home directory holding .claude, .codex and .dsh")
	dry := flags.Bool("dry-run", false, "build and report without replacing anything")
	if flags.Parse(args) != nil {
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	rep, e := rebuild(ctx, *dir, *logs, *dry)
	printReport(out, rep)
	if e != nil {
		_, _ = fmt.Fprintln(errw, "error:", e)
		return 1
	}
	return 0
}

func rebuild(ctx context.Context, dir, home string, dry bool) (aiusage.Report, error) {
	dir, e := filepath.EvalSymlinks(dir)
	if e != nil {
		return aiusage.Report{}, fmt.Errorf("data directory: %w", e)
	}
	owner := uint32(os.Geteuid())
	if up, e := running(dir); e != nil {
		return aiusage.Report{}, fmt.Errorf("cannot tell whether pax-connectd is running: %w", e)
	} else if up {
		return aiusage.Report{}, errors.New("pax-connectd is running for this data directory: quit the app first")
	}
	// Opening the store takes its instance lock, which the daemon holds while it runs.
	history, e := aiprofile.OpenHistory(filepath.Join(dir, "AIProfiles"), owner, dry)
	if errors.Is(e, aiprofile.Conflict) {
		return aiusage.Report{}, errors.New("pax-connectd is running for this data directory (profile store is locked): quit the app first")
	}
	if e != nil {
		return aiusage.Report{}, fmt.Errorf("profile store: %w", e)
	}
	defer func() { _ = history.Close() }()
	roots := map[string]string{}
	for env, cli := range map[string]string{"CLAUDE_CONFIG_DIR": "claude", "CODEX_HOME": "codex", "DSH_HOME": "dsh"} {
		if v := os.Getenv(env); v != "" {
			roots[cli] = v
		}
	}
	return aiusage.Rebuild(ctx, aiusage.RebuildOptions{Directory: dir, Home: home, Roots: roots, Owner: owner, DryRun: dry,
		Timeline: func(ctx context.Context, earliest map[string]time.Time, dry bool) (aiusage.Resolver, []string, error) {
			var seeds []aiprofile.Seed
			for _, p := range seedProfiles {
				at := earliest[string(p.cli)]
				if at.IsZero() {
					at = time.Now()
				}
				seeds = append(seeds, aiprofile.Seed{CLI: p.cli, ProfileName: p.name, At: at})
			}
			planned, e := history.PlanSeeds(ctx, seeds)
			if e != nil {
				return nil, nil, e
			}
			var notes []string
			for _, v := range planned {
				notes = append(notes, fmt.Sprintf("%s -> %s (x%g) from %s", v.CLI, v.ProfileName, v.Multiplier, time.Unix(0, v.At).Format(time.RFC3339)))
			}
			if len(planned) == 0 {
				notes = append(notes, "switch history already seeded")
			}
			extra := planned
			if !dry {
				if e = history.Write(ctx, planned); e != nil {
					return nil, nil, e
				}
				extra = nil
			}
			t, e := history.Timeline(ctx, extra)
			if e != nil {
				return nil, nil, e
			}
			return func(cli string, at time.Time) aiusage.Attribution {
				a := t.Resolve(aiprofile.CLI(cli), at)
				return aiusage.Attribution{ProfileID: a.ProfileID, ProfileName: a.ProfileName, Multiplier: a.Multiplier, Models: a.Models}
			}, notes, nil
		}})
}

// running reports a daemon answering on this data directory's socket. A leftover socket
// file of a stopped daemon refuses the connection; any other failure is not a "no".
func running(dir string) (bool, error) {
	conn, e := net.DialTimeout("unix", filepath.Join(dir, "run", "paxd.sock"), time.Second)
	if e == nil {
		_ = conn.Close()
		return true, nil
	}
	if errors.Is(e, syscall.ECONNREFUSED) || errors.Is(e, os.ErrNotExist) || errors.Is(e, syscall.ENOTSOCK) {
		return false, nil
	}
	return false, e
}

func printReport(w io.Writer, r aiusage.Report) {
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format+"\n", a...) }
	mode := "REBUILD"
	if r.DryRun {
		mode = "DRY RUN (nothing replaced)"
	}
	p("ai-usage %s", mode)
	if r.Backup != "" {
		p("backup:      %s", r.Backup)
	}
	for _, n := range r.Seeded {
		p("seeded:      %s", n)
	}
	section := func(title string, m map[string]aiusage.CLIReport) {
		p("%s", title)
		for _, cli := range sortedKeys(m) {
			c := m[cli]
			p("  %-7s requests %-7d tokens %-12d cost $%.4f", cli, c.Requests, c.Tokens, float64(c.CostMicro)/1e6)
		}
	}
	if len(r.Lifetime) > 0 || len(r.Details) > 0 {
		section("retained requests (from "+r.WindowFrom.Format("2006-01-02")+"):", r.Details)
		section("all hourly buckets:", r.Lifetime)
		carried := 0
		for _, n := range r.Carried {
			carried += n
		}
		p("carried over from old rows (log gone): %d %v", carried, r.Carried)
		p("buckets older than the window: %d kept from the old database, %d rescan-only dropped", r.OldBuckets, r.DroppedBuckets)
		if r.OldBuckets > 0 {
			p("  note: those hourly sums cannot be repriced per component; only their multiplier and profile were recomputed")
		}
		if len(r.UnknownModels) == 0 {
			p("models missing from the price table: none")
		}
		for _, m := range r.UnknownModels {
			p("model missing from the price table (cost 0): %s %s, %d requests", m.CLI, m.Model, m.Requests)
		}
		p("buckets equal details: %v", r.Reconciled)
		for _, m := range r.Mismatches {
			p("  mismatch: %s", m)
		}
		p("quick_check: %s", r.QuickCheck)
	}
	if r.Replaced {
		p("replaced ai_usage.db")
	}
}

func sortedKeys(m map[string]aiusage.CLIReport) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
