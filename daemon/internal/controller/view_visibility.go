package controller

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	paxv1 "pax-connect/daemon/gen/proto/paxv1"
)

// viewIDs are the App pages a Watch subscriber may report as visible (protocol.md §4.2 R7).
var viewIDs = map[string]bool{"dashboard": true, "traffic": true, "logs": true, "ai_sessions": true, "ai_overview": true, "ai_logs": true, "ai_profiles": true}

// SetViewVisibility replaces the reported visible page set. With no Watch subscriber the set
// is treated as empty; the last subscriber leaving clears it. The AI usage cadence reads it
// (ai_usage_push.go); other producers follow with ticket 104.
func (c *Controller) SetViewVisibility(_ context.Context, req *paxv1.ViewVisibility) (*emptypb.Empty, error) {
	next := make(map[string]bool, len(req.GetViews()))
	for _, id := range req.GetViews() {
		if !viewIDs[id] {
			return nil, status.Errorf(codes.InvalidArgument, "unknown view %q", id)
		}
		next[id] = true
	}
	c.mu.Lock()
	c.views = next
	c.mu.Unlock()
	c.aiUsageViewsChanged()
	return &emptypb.Empty{}, nil
}

// ViewVisible reports whether the App declared the page visible on a live Watch.
func (c *Controller) ViewVisible(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.subscribers) > 0 && c.views[id]
}
