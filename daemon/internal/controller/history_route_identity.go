package controller

import "pax-connect/daemon/internal/config"

// rememberHistoryRoute shares the existing bounded runtime-tag index. Versions
// stay available for late observations after edits, switches and catalog removal.
// A name is frozen once per runtime version; already attributed facts keep it.
func (c *Controller) rememberHistoryRoute(p config.RouteConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.data == nil {
		return
	}
	tag := config.RuntimeTag(p)
	if c.data.routeNames == nil {
		c.data.routeNames = map[string]string{}
	}
	if _, exists := c.data.routeNames[tag]; exists {
		return
	}
	if len(c.data.tags) >= 10000 {
		return
	}
	if c.data.tags == nil {
		c.data.tags = map[string]string{}
	}
	c.data.tags[tag] = p.ID
	c.data.routeNames[tag] = p.Name
}
