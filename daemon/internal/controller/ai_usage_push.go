package controller

import (
	"context"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/aiusage"
	"pax-connect/daemon/internal/cadence"
)

// ConfigureAIUsage advertises a successfully opened local collector, ties its cadence to
// the App's reported page visibility and starts pushing its state on Watch (protocol.md
// §4.2 R6, R7). The push stops with ctx.
func (c *Controller) ConfigureAIUsage(ctx context.Context, store *aiusage.Store) {
	c.mu.Lock()
	c.aiUsage = true
	c.aiStore = store
	c.mu.Unlock()
	store.SetForeground(c.aiUsageForeground)
	go c.pushAIUsage(ctx, store)
}

// aiUsageForeground is true while a page that shows AI usage is visible on a live Watch.
// Without a subscriber nothing is visible, so the collector falls back to its slow cadence.
func (c *Controller) aiUsageForeground() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.subscribers) > 0 && (c.views["ai_sessions"] || c.views["ai_overview"] || c.views["ai_logs"] || c.views["dashboard"])
}

// aiUsageViewsChanged wakes the collector when the AI pages turn visible, so a page opened
// during the slow cadence refreshes at once instead of after the remaining wait.
func (c *Controller) aiUsageViewsChanged() {
	c.mu.Lock()
	store := c.aiStore
	c.mu.Unlock()
	if store == nil {
		return
	}
	foreground := c.aiUsageForeground()
	if was := c.aiForeground.Swap(foreground); foreground && !was {
		store.Wake()
	}
}

// pushAIUsage sends the AI usage state on Watch whenever it differs from the last one sent:
// revision, generation and the status fields. At most one event per cadence.AIUsagePublish;
// changes inside the window merge into the latest value.
func (c *Controller) pushAIUsage(ctx context.Context, store *aiusage.Store) {
	last := store.State()
	for {
		select {
		case <-ctx.Done():
			return
		case <-store.Changes():
		}
		if state := store.State(); state != last {
			last = state
			c.mu.Lock()
			c.snapshotSeq++
			c.current.SnapshotVersion = c.snapshotSeq
			c.fanoutLocked(&pb.WatchEvent{Seq: c.snapshotSeq, SnapshotVersion: c.snapshotSeq, Payload: &pb.WatchEvent_AiUsage{AiUsage: usageState(state)}})
			c.mu.Unlock()
		}
		if !cadence.Wait(ctx, cadence.AIUsagePublish) {
			return
		}
	}
}
