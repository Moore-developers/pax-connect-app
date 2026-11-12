package controller

import (
	"context"
	"pax-connect/daemon/internal/config"
)

func (c *Controller) pruneRemoved(ctx context.Context, next *config.Catalog) bool {
	c.mu.Lock()
	active := c.current.GetCurrentRoute().GetId()
	c.mu.Unlock()
	protected := map[string]bool{active: true}
	for _, r := range next.Routes {
		if r.Chain != nil {
			protected[r.Chain.FrontID] = true
			protected[r.Chain.FinalID] = true
			if r.Config.ID == active {
				protected[r.Config.ID] = true
			}
		}
	}
	changed := false
	kept := next.Routes[:0]
	for _, r := range next.Routes {
		if c.tls != nil && r.IsSubscription() && r.SourceRemoved {
			view, err := c.tls.Resolve(ctx, r.Config)
			if err != nil || view.UserOverride {
				protected[r.Config.ID] = true
			}
		}
		if r.IsSubscription() && r.SourceRemoved && !r.Hidden && len(r.Overrides) == 0 && r.NameOverride == "" && !protected[r.Config.ID] {
			if next.SelectedID == r.Config.ID {
				next.SelectedID = ""
			}
			changed = true
			continue
		}
		kept = append(kept, r)
	}
	next.Routes = kept
	if changed {
		next.Revision++
		next.PruneGroupMembers()
	}
	return changed
}
