package controller

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

type historyApplicationInventory struct {
	At   time.Time
	Apps []config.RoutingApplication
}

// Reuse the installed-bundle catalogue used by rule selection. The inventory
// is frozen per read, and does not rewrite saved connection identity evidence.
func (c *Controller) historyUserApplications(ctx context.Context) ([]config.RoutingApplication, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Log queries run outside the legacy owner goroutine; the inventory cache needs its own lock.
	if c.history == nil {
		return c.applicationInventoryQuery(ctx), nil
	}
	c.history.appMu.Lock()
	cached := c.history.applicationInventory
	recent := cached != nil && time.Since(cached.At) < 5*time.Minute
	c.history.appMu.Unlock()
	if recent {
		return cached.Apps, nil
	}
	apps := c.applicationInventoryQuery(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.history.appMu.Lock()
	c.history.applicationInventory = &historyApplicationInventory{At: time.Now(), Apps: apps}
	c.history.appMu.Unlock()
	return apps, nil
}

func (c *Controller) applicationInventoryQuery(ctx context.Context) []config.RoutingApplication {
	query := c.localRoutingApplications
	if c.routing != nil && c.routing.applicationQuery != nil {
		query = c.routing.applicationQuery
	}
	apps := query(ctx)
	if len(apps) > 512 {
		apps = apps[:512]
	}
	return apps
}
func userApplicationMatch(id, path string, apps []config.RoutingApplication) (config.RoutingApplication, bool) {
	for _, app := range apps {
		if !app.Bundle || strings.HasPrefix(app.Path, "/System/") || strings.HasPrefix(app.Path, "/Library/Apple/") {
			continue
		}
		if id != "" && id == app.ID || path != "" && app.Path != "" && (path == app.Path || strings.HasPrefix(filepath.Clean(path), filepath.Clean(app.Path)+"/")) {
			return app, true
		}
	}
	return config.RoutingApplication{}, false
}

func logSystemSource(v *pb.LogRecord) (string, string) {
	f := v.Flow
	if f == nil {
		return v.SystemCategory, v.CategoryName
	}
	id, name := f.ApplicationId, f.Application
	if f.Process != "" {
		id = "process:" + f.Process
		if name == "" {
			name = filepath.Base(f.Process)
		}
	}
	if f.Purpose == "dns" {
		return "network/dns", "域名解析"
	}
	if id == "" {
		id = "unknown"
	}
	if name == "" {
		name = "未识别进程"
	}
	return "network/" + id, name
}
