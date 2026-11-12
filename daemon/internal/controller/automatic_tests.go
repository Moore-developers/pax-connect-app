package controller

import (
	"context"
	"time"

	"google.golang.org/grpc/status"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

type automaticRouteTest struct {
	id       string
	revision uint64
}

// enqueueAutomaticTests is only called after explicit import or saved TLS consent.
// Page opening never schedules traffic. Hardware-sized workers and a bounded queue share the
// ordinary tester's global slots; queued items freeze the saved record revision.
func (c *Controller) enqueueAutomaticTests(items []automaticRouteTest) {
	if c.tests == nil {
		return
	}
	for _, item := range items {
		select {
		case c.tests.automatic <- item:
		case <-c.tests.lifetime.Done():
			return
		default:
			// The catalog's 1000-route limit bounds the queue; do not grow a goroutine per item.
			return
		}
	}
}
func (c *Controller) automaticTestLoop(owner *testOwner) {
	defer owner.workers.Done()
	for {
		select {
		case <-owner.lifetime.Done():
			return
		case item := <-owner.automatic:
			c.runAutomaticTest(owner, item)

		}
	}
}

// An in-flight result may belong to the pre-consent version. Await it, then
// retry the frozen record revision rather than losing the saved-policy request.
func (c *Controller) runAutomaticTest(owner *testOwner, item automaticRouteTest) {
	for attempt := 0; attempt < 2; attempt++ {
		ack, err := c.TestRoute(owner.lifetime, &paxv1.TestRouteRequest{RouteId: item.id, ExpectedRouteRevision: item.revision})
		if err != nil {
			code := status.Convert(err).Message()
			if code == "tls_confirmation_required" {
				// A source's insecure flag is a suggestion, never automatic consent.
				_ = c.query(owner.lifetime, func(ctx context.Context) {
					record, ok := c.routes.catalog.Find(item.id)
					if !ok || record.Revision != item.revision {
						return
					}
					view, resolveErr := c.tls.Resolve(ctx, record.Config)
					if resolveErr != nil {
						return
					}
					c.routes.testResults[item.id] = routeTestResult{at: time.Now(), code: code, revision: item.revision, configRevision: connectionVersion(record, view)}
					c.persistMeasurements(ctx)
					c.publishCatalog(ctx)
				})
			}
			return
		}
		ticker := time.NewTicker(50 * time.Millisecond)
		for {
			owner.mu.Lock()
			_, active := owner.ops[ack.OperationId]
			owner.mu.Unlock()
			if !active {
				break
			}
			select {
			case <-owner.lifetime.Done():
				ticker.Stop()
				return
			case <-ticker.C:
			}
		}
		ticker.Stop()
		repeat := false
		_ = c.query(owner.lifetime, func(ctx context.Context) {
			record, ok := c.routes.catalog.Find(item.id)
			if !ok || record.Revision != item.revision {
				return
			}
			view := config.SourceTLSPolicy(record.Config)
			if c.tls != nil && record.Config.Security == "tls" {
				resolved, err := c.tls.Resolve(ctx, record.Config)
				if err != nil {
					return
				}
				view = resolved
			}
			result, ok := c.routes.testResults[item.id]
			repeat = !ok || result.configRevision != connectionVersion(record, view)
		})
		if !repeat {
			return
		}

	}
}
