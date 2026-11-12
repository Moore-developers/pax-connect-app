package controller

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/aiusage"
)

func TestSetViewVisibilityRequiresWatchSubscriber(t *testing.T) {
	c := New("test", "fp")
	if _, err := c.SetViewVisibility(context.Background(), &paxv1.ViewVisibility{Views: []string{"traffic", "ai_logs"}}); err != nil {
		t.Fatal(err)
	}
	if c.ViewVisible("traffic") {
		t.Fatal("no Watch subscriber must mean nothing is visible")
	}
	c.subscribers[make(chan *paxv1.WatchEvent)] = make(chan struct{})
	if !c.ViewVisible("traffic") || !c.ViewVisible("ai_logs") || c.ViewVisible("logs") {
		t.Fatal("reported set must be visible with a subscriber")
	}
	if _, err := c.SetViewVisibility(context.Background(), &paxv1.ViewVisibility{}); err != nil || c.ViewVisible("traffic") {
		t.Fatalf("empty set replaces the previous one: %v", err)
	}
}

func TestSetViewVisibilityRejectsUnknownView(t *testing.T) {
	c := New("test", "fp")
	_, err := c.SetViewVisibility(context.Background(), &paxv1.ViewVisibility{Views: []string{"nope"}})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("got %v", err)
	}
}

func TestPushContractFieldsAreMapped(t *testing.T) {
	h := historyState(historyMetadata{RouteHistoryRevision: 7})
	if h.RouteHistoryRevision != 7 {
		t.Fatalf("route_history_revision: %+v", h)
	}
	a := usageState(aiusage.State{Revision: 3, Generation: 2})
	if a.Revision != 3 || a.Generation != 2 {
		t.Fatalf("ai usage state: %+v", a)
	}
}
