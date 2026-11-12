package controller

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// Rule candidates are installed bundles, not the broader traffic source list.
func (c *Controller) localRoutingApplications(ctx context.Context) []config.RoutingApplication {
	roots := []string{"/Applications"}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, "Applications"))
	}
	return c.installedRoutingApplications(ctx, roots)
}
func (c *Controller) installedRoutingApplications(ctx context.Context, roots []string) []config.RoutingApplication {
	values := map[string]config.RoutingApplication{}
	domainBudget := 4096
	add := func(f config.FlowFact) {
		source := config.ClassifyTrafficSource(f)
		if source.Kind != "app" {
			return
		}
		if f.Process == "" || !filepath.IsAbs(f.Process) || ctx.Err() != nil {
			return
		}
		// Revalidate current on-disk identity, rather than trust a historical display name.
		identifyHistoryApplicationContext(ctx, &f)
		if f.ApplicationID == "" || strings.HasPrefix(f.ApplicationID, "process:") {
			return
		}
		path := f.Process
		bundle := false
		for p := filepath.Dir(path); p != "/" && p != "."; p = filepath.Dir(p) {
			if strings.HasSuffix(p, ".app") {
				path = p
				bundle = true
			}
		}
		if !bundle {
			return
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || strings.HasPrefix(resolved, "/System/") || strings.HasPrefix(resolved, "/Library/Apple/") {
			return
		}
		if info, err := os.Stat(resolved); err != nil || !info.IsDir() {
			return
		}
		a, exists := values[f.ApplicationID]
		if !exists {
			a = config.RoutingApplication{ID: f.ApplicationID, Name: f.Application, Path: path, Bundle: bundle}
		}
		if len(values) < 512 || exists {
			values[a.ID] = a
		}
	}
	// Installed main bundles can be chosen before their first observed connection.
	for _, root := range roots {
		entries, _ := os.ReadDir(root)
		for _, entry := range entries {
			if ctx.Err() != nil {
				break
			}
			if strings.HasSuffix(entry.Name(), ".app") {
				add(config.FlowFact{Process: filepath.Join(root, entry.Name(), "Contents/MacOS/pax-identity")})
			} else if entry.IsDir() {
				nested, _ := os.ReadDir(filepath.Join(root, entry.Name()))
				for _, app := range nested {
					if strings.HasSuffix(app.Name(), ".app") {
						add(config.FlowFact{Process: filepath.Join(root, entry.Name(), app.Name(), "Contents/MacOS/pax-identity")})
					}
				}
			}
		}
	}
	if job, e := c.logJob(); e == nil && ctx.Err() == nil {
		if source, ok := job.logs.(interface {
			EachRecentConnection(context.Context, int64, int64, int, func(historystore.LogConnection) error) error
		}); ok {
			_ = source.EachRecentConnection(ctx, time.Now().Add(-24*time.Hour).UnixMilli(), time.Now().UnixMilli(), 10000, func(row historystore.LogConnection) error {
				var summary config.LogConnectionSummary
				if e := json.Unmarshal(row.Body, &summary); e != nil {
					return e
				}
				f := summary.Fact
				source := config.ClassifyTrafficSource(f)
				if source.Kind != "app" {
					return nil
				}
				if a, ok := values[f.ApplicationID]; ok {
					if f.Domain != "" && len(a.Domains) < 32 && domainBudget > 0 && !slices.Contains(a.Domains, f.Domain) {
						if v, e := config.NormalizeRoutingTarget("domain", f.Domain); e == nil {
							a.Domains = append(a.Domains, v)
							domainBudget--
							values[a.ID] = a
						}
					}
				}
				return nil
			})
		}
	}

	out := make([]config.RoutingApplication, 0, len(values))
	for _, a := range values {
		slices.Sort(a.Domains)
		out = append(out, a)
	}
	slices.SortFunc(out, func(a, b config.RoutingApplication) int {
		if n := strings.Compare(a.Name, b.Name); n != 0 {
			return n
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}
