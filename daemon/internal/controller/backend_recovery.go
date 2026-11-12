package controller

import (
	"context"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
)

// recoverBackendState runs once on the product owner, before RPC admission. A
// new paxd has no frozen profile/binding; observing STARTED cannot recreate it.
// Preserve the runtime and publish uncertainty until an explicit OFF/ON.
func (c *Controller) recoverBackendState(connection *connectionState) {
	observer, ok := c.coreSession.(coreObserver)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.proxy.lifetime, 2*time.Second)
	defer cancel()
	actual, err := observer.ObserveCore(ctx)
	if err != nil && err.Error() == "core_required" || err == nil && actual.Status == "IDLE" {
		return
	}
	connection.running = true
	connection.cleanupFailed = true
	c.proxy.desired.Store(true)
	c.proxyState(pb.ProtectionStatus_PROTECTION_STATUS_DEGRADED, "runtime_observation_unavailable", nil)
}
