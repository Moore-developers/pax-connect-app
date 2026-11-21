package singbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"pax-connect/daemon/internal/config"
)

// maintainFixedRoutes is called only by the root lifecycle owner. Network
// workers return bounded verdicts; they never change rules or close sessions.
func (r *CoreRuntime) maintainFixedRoutes(ctx context.Context) {
	if r.fixedPaused || r.Status() != "STARTED" {
		return
	}
	r.fixed.maintain(ctx)
	for tag := range r.fixed.blocked {
		r.reportFixedRoute(tag, true)
	}
	instance := r.current().Instance()
	if instance == nil {
		return
	}
	tag, blocked, changed := r.fixedHealth.poll(time.Now(), r.pathVersion.Load(), r.fixed.activeTags(ctx), instance.Box().Outbound().Outbound)
	if !changed {
		return
	}
	_, pending, err := r.fixed.protect(ctx, tag, blocked)
	if !blocked && !pending && err == nil {
		r.recordFixedRoute("fixed_route_recovered", "指定线路 "+r.fixedRouteName(tag)+" 已恢复，新连接可再次使用", "info")
		return
	}
	r.reportFixedRoute(tag, true)
}

func (r *CoreRuntime) reportFixedRoute(tag string, blocked bool) {
	state := r.fixed.status[tag]
	if state == nil || state.reported && state.reportedClosed == state.closed && state.reportedPending == state.pending {
		return
	}
	name := r.fixedRouteName(tag)
	message, code := "", "fixed_route_unavailable"
	if blocked {
		message = fmt.Sprintf("指定线路 %s 不可用，已确认关闭 %d 条连接", name, state.closed)
	}
	if state.pending {
		message, code = "指定线路 "+name+" 已拦截，连接关闭待确认", "fixed_route_pending"
	}
	state.reported, state.reportedClosed, state.reportedPending = true, state.closed, state.pending
	r.recordFixedRoute(code, message, "warn")
}

func (r *CoreRuntime) recordFixedRoute(code, message, level string) {
	r.observations.mu.Lock()
	r.observations.append(config.Observation{At: time.Now(), Kind: "route_protection", Code: code, Raw: message, Level: level})
	r.observations.mu.Unlock()
}

func (r *CoreRuntime) fixedRouteName(tag string) string {
	doc, _ := fixedRoutingDocument(r.fixed.baseProfile)
	for i, rule := range fixedRoutingRules(doc, "route") {
		if rule["outbound"] != tag || i >= len(r.fixed.baseSources) {
			continue
		}
		var match config.RoutingMatchEvidence
		if json.Unmarshal([]byte(r.fixed.baseSources[i]), &match) == nil && match.FixedRouteName != "" {
			return match.FixedRouteName
		}
	}
	return tag
}
