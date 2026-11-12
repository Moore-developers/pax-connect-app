package core

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type commandRunner func(context.Context, string, ...string) (string, error)

func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}
func fields(raw string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), ":"); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}
func cleanupProxy(ctx context.Context, port int, run commandRunner) error {
	raw, err := run(ctx, "/usr/sbin/networksetup", "-listallnetworkservices")
	if err != nil {
		return errors.New("core_proxy_list_failed")
	}
services:
	for _, service := range strings.Split(raw, "\n") {
		service = strings.TrimSpace(service)
		if service == "" || strings.HasPrefix(service, "An asterisk") {
			continue
		}
		service = strings.TrimPrefix(service, "*")
		for _, kind := range []string{"webproxy", "securewebproxy", "socksfirewallproxy"} {
			raw, err = run(ctx, "/usr/sbin/networksetup", "-get"+kind, service)
			if err != nil {
				slog.Warn("core_proxy_read_failed", "operation_id", "startup", "snapshot_version", 0, "reason", "service_skipped")
				continue services
			}
			f := fields(raw)
			if f["Enabled"] == "Yes" && f["Server"] == "127.0.0.1" && f["Port"] == strconv.Itoa(port) {
				if _, err = run(ctx, "/usr/sbin/networksetup", "-set"+kind+"state", service, "off"); err != nil {
					return errors.New("core_proxy_clear_failed")
				}
			}
		}
	}
	return nil
}

// releaseProxy only clears endpoints still pointing to this core. Closing an
// upstream instance is not sufficient evidence that DynamicStore has converged.
func releaseProxy(ctx context.Context, port int, run commandRunner) error {
	if err := cleanupProxy(ctx, port, run); err != nil {
		return err
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		raw, err := run(ctx, "/usr/sbin/scutil", "--proxy")
		if err != nil {
			return errors.New("core_proxy_observation_failed")
		}
		active, err := effectivePaxProxy(raw, port)
		if err != nil {
			return err
		}
		if !active {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("core_proxy_release_unconfirmed")
		case <-ticker.C:
		}
	}
}

// scutil nests per-interface and supplemental proxy dictionaries. Keep each
// dictionary separate so a third-party endpoint cannot hide Pax in another scope.
func effectivePaxProxy(raw string, port int) (bool, error) {
	fail := errors.New("core_proxy_observation_failed")
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	if len(raw) > 1024*1024 || len(lines) < 2 || strings.TrimSpace(lines[0]) != "<dictionary> {" {
		return false, fail
	}
	stack := []map[string]string{{}}
	active := false
	for _, rawLine := range lines[1:] {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if len(stack) == 0 {
			return false, fail
		}
		if line == "}" {
			state := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, kind := range []string{"HTTP", "HTTPS", "SOCKS"} {
				enabled, exists := state[kind+"Enable"]
				if !exists || enabled == "0" {
					continue
				}
				if enabled != "1" || state[kind+"Proxy"] == "" {
					return false, fail
				}
				p, err := strconv.Atoi(state[kind+"Port"])
				if err != nil || p <= 0 || p > 65535 {
					return false, fail
				}
				active = active || state[kind+"Proxy"] == "127.0.0.1" && p == port
			}
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return false, fail
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch value {
		case "<dictionary> {":
			stack = append(stack, map[string]string{})
		case "<array> {":
			stack = append(stack, nil)
		default:
			if strings.HasSuffix(value, "{") {
				return false, fail
			}
			state := stack[len(stack)-1]
			if state != nil {
				if _, duplicate := state[key]; duplicate {
					return false, fail
				}
				state[key] = value
			}
		}
	}
	if len(stack) != 0 {
		return false, fail
	}
	return active, nil
}
