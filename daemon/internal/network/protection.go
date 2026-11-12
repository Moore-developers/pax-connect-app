package network

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"sort"
	"strings"
)

// ConflictKind distinguishes observed ownership from an unavailable observation.
type ConflictKind string

// ErrNoRoute means the kernel answered that no route exists for this family.
var errObservationUnavailable = errors.New("network_observation_unavailable")

var ErrNoRoute = errors.New("route absent")

const (
	ConflictNone             ConflictKind = "none"
	ConflictPax              ConflictKind = "pax_tun"
	ConflictOtherUTUN        ConflictKind = "other_utun"
	ConflictUnknown          ConflictKind = "route_unknown"
	ConflictObservationError ConflictKind = "network_observation_unavailable"
)

// ConflictResult contains only the routing evidence needed by the controller.
type ConflictResult struct {
	Kind      ConflictKind
	Interface string
	Target    string
}

// ClassifyConflict applies ownership before reporting an external utun. A Pax
// interface is identified by its configured address, so an old v2rayN/another
// VPN utun is never silently treated as our own interface.
func ClassifyConflict(routeInterfaces map[string]string, paxInterface string, observed bool) ConflictResult {
	if !observed {
		return ConflictResult{Kind: ConflictObservationError}
	}
	targets := make([]string, 0, len(routeInterfaces))
	for target := range routeInterfaces {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	unknownTarget := ""
	paxTarget := ""
	for _, target := range targets {
		name := routeInterfaces[target]
		if name == "" {
			if unknownTarget == "" {
				unknownTarget = target
			}
			continue
		}
		if !strings.HasPrefix(name, "utun") {
			continue
		}
		if name != paxInterface {
			return ConflictResult{Kind: ConflictOtherUTUN, Interface: name, Target: target}
		}
		// Keep looking: a stale Pax interface is harmless only when no other
		// utun is routing traffic. External ownership always wins.
		if paxTarget == "" {
			paxTarget = target
		}
	}
	if unknownTarget != "" {
		return ConflictResult{Kind: ConflictUnknown, Target: unknownTarget}
	}
	if paxTarget != "" {
		return ConflictResult{Kind: ConflictPax, Interface: paxInterface, Target: paxTarget}
	}
	return ConflictResult{Kind: ConflictNone}
}

// ParseRouteInterface extracts the interface chosen by the actual macOS route lookup.
func ParseRouteInterface(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if key, value, ok := strings.Cut(strings.TrimSpace(line), ":"); ok && key == "interface" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// RouteInterface queries kernel routing, ignoring HTTP proxy environment variables.
func RouteInterface(ctx context.Context, target string) (string, error) {
	args := []string{"-n", "get", target}
	if strings.Contains(target, ":") {
		args = []string{"-n", "get", "-inet6", target}
	}
	output, err := readRouteCommand(ctx, args...)
	if err != nil {
		return "", err
	}
	return ParseRouteInterface(string(output)), nil
}

// macOS route can report ESRCH on stderr while exiting successfully.
func routeCommandResult(output, stderr []byte, commandErr error) ([]byte, error) {
	if strings.Contains(string(stderr), "not in table") {
		return nil, ErrNoRoute
	}
	if commandErr != nil || ParseRouteInterface(string(output)) == "" {
		return nil, errObservationUnavailable
	}
	return output, nil
}

func readRouteCommand(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "/sbin/route", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return routeCommandResult(output, stderr.Bytes(), err)
}

func assessConflict(ctx context.Context, route func(context.Context, string) (string, error), tun func() (string, error)) ConflictResult {
	values := make(map[string]string)
	allObserved := true
	for _, target := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		name, err := route(ctx, target)
		if err != nil {
			// A native IPv6 default route is optional; our TUN supplies the
			// family on startup. This is distinct from a failed observation.
			if strings.Contains(target, ":") && errors.Is(err, ErrNoRoute) {
				continue
			}
			allObserved = false
			continue
		}
		values[target] = name
	}
	pax, err := tun()
	if err != nil {
		return ConflictResult{Kind: ConflictObservationError}
	}
	known := ClassifyConflict(values, pax, true)
	if known.Kind == ConflictOtherUTUN {
		return known
	}
	if !allObserved {
		return ConflictResult{Kind: ConflictObservationError}
	}
	return known
}
