package environment

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

const dnsProbeCount = 12

// DNSFlow is an intercepted query or exact-domain proxy connection with response evidence.
type DNSFlow struct {
	Domain, Upstream, Outbound, Pool string
	RemoteRoute                      string
	RemoteProxy, Responded           bool
	StartedAt, UpstreamAt            time.Time
}

// DNSPathProvider reads only live query facts; no configured route is a substitute.
type DNSPathProvider interface {
	DNSPath(names []string, from, to time.Time) []Check
}

func dnsProbeNames(id string) []string {
	names := make([]string, dnsProbeCount)
	for i := range names {
		names[i] = randomToken() + "." + id + ".bash.ws"
	}
	return names
}

// DNSPathChecks correlates exact names and counts coverage without exporting query tokens.
func DNSPathChecks(names []string, flows []DNSFlow, available bool, from, to time.Time) []Check {
	summary := Check{Kind: "dns_path", Target: "dns", State: "unobserved", At: time.Now()}
	if !available {
		summary.State = "unavailable"
		summary.ErrorCode = "live_view_unavailable"
	}
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[strings.ToLower(strings.TrimSuffix(name, "."))] = true
	}
	covered := map[string]bool{}
	unknown, proxied, direct, bypass := 0, 0, 0, 0
	details := []Check{}
	seen := map[string]bool{}
	for _, flow := range flows {
		name := strings.ToLower(strings.TrimSuffix(flow.Domain, "."))
		if !available || !wanted[name] || flow.StartedAt.Before(from) || flow.StartedAt.After(to) {
			continue
		}
		state := "unknown"
		if flow.RemoteProxy && flow.Responded && validDNSProxy(flow.Outbound) {
			state = "remote_proxy"
		} else if !flow.UpstreamAt.IsZero() && !flow.UpstreamAt.Before(from) && !flow.UpstreamAt.After(to) {
			switch {
			case flow.Pool == "overseas" && (flow.Upstream == "1.1.1.1" || flow.Upstream == "8.8.8.8"):
				if flow.Outbound == "direct" {
					state = "bypass"
				} else if validDNSProxy(flow.Outbound) {
					state = "proxy"
				}
			case flow.Pool == "domestic" && (flow.Upstream == "223.5.5.5" || flow.Upstream == "119.29.29.29"):
				if flow.Outbound == "direct" {
					state = "policy_direct"
				} else if validDNSProxy(flow.Outbound) {
					state = "proxy"
				}
			}
		}
		switch state {
		case "proxy", "remote_proxy":
			proxied++
		case "policy_direct":
			direct++
		case "bypass":
			bypass++
		default:
			unknown++
			continue
		}
		covered[name] = true
		value, _ := json.Marshal(struct {
			Server   string `json:"server"`
			Outbound string `json:"outbound"`
			Pool     string `json:"pool"`
			Route    string `json:"route,omitempty"`
		}{flow.Upstream, flow.Outbound, flow.Pool, flow.RemoteRoute})
		key := state + string(value)
		if !seen[key] && len(details) < 4 {
			details = append(details, Check{Kind: "dns_upstream", Target: "dns", State: state, Value: string(value), At: flow.UpstreamAt})
			seen[key] = true
		}
	}
	if available {
		switch {
		case bypass > 0:
			summary.State = "bypass"
		case len(covered) > 0 && (len(covered) != dnsProbeCount || len(wanted) != dnsProbeCount || unknown > 0):
			summary.State = "partial"
		case len(covered) == dnsProbeCount && proxied > 0 && direct > 0:
			summary.State = "mixed"
		case len(covered) == dnsProbeCount && proxied > 0:
			summary.State = "proxy"
		case len(covered) == dnsProbeCount && direct > 0:
			summary.State = "policy_direct"
		}
	}
	value, _ := json.Marshal(struct {
		Expected   int `json:"expected"`
		Observed   int `json:"observed"`
		Unverified int `json:"unverified"`
	}{dnsProbeCount, len(covered), unknown})
	summary.Value = string(value)
	return append([]Check{summary}, details...)
}

func validDNSProxy(tag string) bool {
	if tag == "proxy" {
		return true
	}
	if !strings.HasPrefix(tag, "pax-node-") || len(tag) > 160 {
		return false
	}
	for _, c := range tag {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

// Allow the existing ingest window to publish responses; cancellation never becomes confirmation.
func collectDNSPath(ctx context.Context, provider EvidenceProvider, names []string, from, to time.Time) []Check {
	source, ok := provider.(DNSPathProvider)
	if !ok || len(names) != dnsProbeCount {
		return DNSPathChecks(names, nil, false, from, to)
	}
	deadline := time.NewTimer(routeCollectWindow)
	defer deadline.Stop()
	ticker := time.NewTicker(routePollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return DNSPathChecks(names, nil, false, from, to)
		}
		checks := source.DNSPath(names, from, to)
		if len(checks) > 0 && checks[0].State != "partial" && checks[0].State != "unobserved" {
			return checks
		}
		select {
		case <-ctx.Done():
			return DNSPathChecks(names, nil, false, from, to)
		case <-deadline.C:
			return source.DNSPath(names, from, to)
		case <-ticker.C:
		}
	}
}
