package network

import (
	"context"
	"fmt"
	"os"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

// StartRouteWatch subscribes to PF_ROUTE with the default debounce.
func StartRouteWatch(ctx context.Context) *RouteWatch {
	return WatchRoutes(ctx, OpenRouteSocket, RouteWatchOptions{})
}

type routeSocket struct {
	file *os.File
	buf  []byte
}

// OpenRouteSocket opens a PF_ROUTE listener. It needs no privilege.
func OpenRouteSocket() (RouteMessageSource, error) {
	fd, err := unix.Socket(unix.AF_ROUTE, unix.SOCK_RAW, 0)
	if err != nil {
		return nil, fmt.Errorf("open route socket: %w", err)
	}
	unix.CloseOnExec(fd)
	if err := unix.SetNonblock(fd, true); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("route socket nonblocking: %w", err)
	}
	// A nonblocking descriptor joins the runtime poller, so Close unblocks Read.
	return &routeSocket{file: os.NewFile(uintptr(fd), "pf_route"), buf: make([]byte, 1<<16)}, nil
}

func (s *routeSocket) Next() error {
	for {
		n, err := s.file.Read(s.buf)
		if err != nil {
			return err
		}
		messages, _ := route.ParseRIB(route.RIBTypeRoute, s.buf[:n])
		for _, message := range messages {
			if RouteMessageRelevant(message) {
				return nil
			}
		}
	}
}

func (s *routeSocket) Close() error { return s.file.Close() }

// RouteMessageRelevant keeps address, link and non-cache route changes. RTM_GET replies are
// excluded because our own `route get` sampling would otherwise retrigger itself, and
// ARP/NDP/cloned/host entries churn constantly without moving the egress.
func RouteMessageRelevant(message route.Message) bool {
	switch m := message.(type) {
	case *route.InterfaceAddrMessage:
		return m.Type == unix.RTM_NEWADDR || m.Type == unix.RTM_DELADDR
	case *route.InterfaceMessage:
		return m.Type == unix.RTM_IFINFO || m.Type == unix.RTM_IFINFO2
	case *route.RouteMessage:
		if m.Type != unix.RTM_ADD && m.Type != unix.RTM_DELETE && m.Type != unix.RTM_CHANGE {
			return false
		}
		return m.Flags&(unix.RTF_LLINFO|unix.RTF_WASCLONED|unix.RTF_HOST|unix.RTF_MULTICAST|unix.RTF_BROADCAST) == 0
	}
	return false
}
