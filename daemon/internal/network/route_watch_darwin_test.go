package network

import (
	"context"
	"testing"
	"time"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

func TestRouteMessageRelevantFiltersNoise(t *testing.T) {
	cases := []struct {
		name    string
		message route.Message
		want    bool
	}{
		{"default route added", &route.RouteMessage{Type: unix.RTM_ADD, Flags: unix.RTF_UP | unix.RTF_GATEWAY}, true},
		{"route deleted", &route.RouteMessage{Type: unix.RTM_DELETE, Flags: unix.RTF_UP}, true},
		{"route changed", &route.RouteMessage{Type: unix.RTM_CHANGE}, true},
		{"own route get reply", &route.RouteMessage{Type: unix.RTM_GET, Flags: unix.RTF_UP}, false},
		{"arp entry", &route.RouteMessage{Type: unix.RTM_ADD, Flags: unix.RTF_HOST | unix.RTF_LLINFO}, false},
		{"cloned route", &route.RouteMessage{Type: unix.RTM_DELETE, Flags: unix.RTF_WASCLONED}, false},
		{"address added", &route.InterfaceAddrMessage{Type: unix.RTM_NEWADDR}, true},
		{"address removed", &route.InterfaceAddrMessage{Type: unix.RTM_DELADDR}, true},
		{"link state", &route.InterfaceMessage{Type: unix.RTM_IFINFO}, true},
	}
	for _, c := range cases {
		if got := RouteMessageRelevant(c.message); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

// Real PF_ROUTE needs no privilege; this proves Close unblocks a pending read so daemon
// shutdown cannot hang on the watcher.
func TestRouteSocketOpensAndCloseUnblocksRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	w := StartRouteWatch(ctx)
	select {
	case <-w.Lost():
		t.Skip("PF_ROUTE unavailable here:", w.Err())
	case <-time.After(200 * time.Millisecond):
	}
	cancel()
	select {
	case <-w.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("closing the route socket did not unblock the reader")
	}
}
