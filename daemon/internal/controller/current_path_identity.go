package controller

import (
	"context"
	"time"

	"pax-connect/daemon/internal/config"
)

// runningBinding describes a committed selection, not the latest saved document.
// Only the product owner writes it. Workers receive immutable copies.
type runningBinding struct {
	route, tag, name, session string
	revision                  uint64
}

func (c *Controller) bindCurrentPath(p config.RouteConfig) {
	c.proxy.running = runningBinding{route: p.ID, tag: config.RuntimeTag(p), name: p.Name, revision: p.ResolvedConfigRevision}
	if observer, ok := c.coreSession.(coreObserver); ok {
		ctx, cancel := context.WithTimeout(c.proxy.lifetime, time.Second)
		defer cancel()
		value, err := observer.ObserveCore(ctx)
		if err == nil && value.Status == "STARTED" && value.SelectedTag == c.proxy.running.tag {
			c.proxy.running.session = value.RuntimeSession
		}
	}
}

func (b runningBinding) valid() bool { return b.route != "" && b.tag != "" }
