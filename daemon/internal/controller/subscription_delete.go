package controller

import (
	"context"
	"errors"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
)

// DeleteSubscription atomically removes a source and all its saved routes,
// including hidden members and local overrides. An active/pending member or a
// saved chain reference prevents deletion; historical usage remains retained.
func (c *Controller) DeleteSubscription(ctx context.Context, req *paxv1.DeleteSubscriptionRequest) (*paxv1.OperationAck, error) {
	return c.mutationRequest(ctx, "delete-subscription", req, req.ClientId, req.RequestId, func() (*paxv1.OperationAck, error) {
		return c.admitNamedRoute(ctx, "route.delete_subscription", "删除订阅", func(ctx context.Context) error {
			source, ok := c.routes.catalog.FindSubscription(req.SubscriptionId)
			if !ok {
				return errors.New("subscription_not_found")
			}
			if source.Revision != req.ExpectedSourceRevision || c.routes.catalog.Revision != req.ExpectedCatalogRevision {
				return errors.New("subscription_stale")
			}
			ids := map[string]bool{}
			for _, route := range c.routes.catalog.Routes {
				if route.SourceID == source.ID {
					ids[route.Config.ID] = true
				}
			}
			for _, r := range c.routes.catalog.Routes {
				if r.Chain != nil && (ids[r.Chain.FrontID] || ids[r.Chain.FinalID]) {
					return errors.New("route_referenced")
				}
			}
			c.mu.Lock()
			active := c.current.GetCurrentRoute().GetId()
			pending := c.current.PendingTargetRouteId
			c.mu.Unlock()
			if ids[active] || ids[pending] {
				return errors.New("subscription_in_use")
			}

			next := c.routes.catalog.Clone()
			kept := next.Routes[:0]
			for _, route := range next.Routes {
				if !ids[route.Config.ID] {
					kept = append(kept, route)
				}
			}
			next.Routes = kept
			sources := next.Subscriptions[:0]
			for _, record := range next.Subscriptions {
				if record.ID != source.ID {
					sources = append(sources, record)
				}
			}
			next.Subscriptions = sources
			if ids[next.SelectedID] {
				next.SelectedID = ""
			}
			next.Revision++
			next.PruneGroupMembers()
			if err := c.saveCatalog(ctx, next); err != nil {
				return err
			}
			// Commit has succeeded; stop unfinished source fetches and tests so they cannot
			// resurrect removed members. Their completion paths already fence existence.
			if c.subs != nil {
				c.subs.mu.Lock()
				for _, run := range c.subs.ops {
					if run.sourceID == source.ID {
						run.cancel()
					}
				}
				c.subs.mu.Unlock()
			}
			if c.tests != nil {
				c.tests.mu.Lock()
				for id := range ids {
					for _, operation := range []string{c.routes.testing[id], c.routes.downloading[id]} {
						if cancel := c.tests.ops[operation]; cancel != nil {
							cancel()
						}
					}
				}
				c.tests.mu.Unlock()
			}
			for id := range ids {
				delete(c.routes.testing, id)
				delete(c.routes.downloading, id)
				delete(c.routes.testResults, id)
				delete(c.routes.downloadResults, id)
			}
			for token, preview := range c.routes.previews {
				if preview.source != nil && preview.source.id == source.ID {
					delete(c.routes.previews, token)
				}
			}
			c.persistMeasurements(ctx)
			c.publishCatalog(ctx)
			return nil
		})
	})
}
