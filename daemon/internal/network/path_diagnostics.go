package network

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PathFinding is a bounded observation, never a guarantee about all app traffic.
type PathFinding struct{ Kind, Subject, State, Value string }

// PathReport contains only proxy endpoints, known process names and fixed-target
// route evidence. PAC URLs, process arguments and remote connections never escape.
type PathReport struct {
	Findings []PathFinding
	Partial  bool
	Changed  bool
}

// PathInspector owns read-only OS observation. The injectable I/O boundaries
// keep diagnostics tests offline and independent of host privileges.
type PathInspector struct {
	Command  func(context.Context, string, ...string) ([]byte, error)
	Dial     func(context.Context, string, string) (net.Conn, error)
	Physical func(context.Context) (Physical, error)
}

// NewPathInspector uses bounded native commands, never a shell or user arguments.
func NewPathInspector() PathInspector {
	return PathInspector{Command: pathCommand, Physical: PhysicalNetwork, Dial: (&net.Dialer{Timeout: 250 * time.Millisecond}).DialContext}
}

func pathCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name == "/sbin/route" {
		return readRouteCommand(ctx, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(pipe, 1<<20+1))
	if len(data) > 1<<20 {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if len(data) > 1<<20 {
		return nil, errors.New("observation_limit")
	}
	return data, errors.Join(readErr, waitErr)
}

// Inspect does not fetch PAC scripts, contact remote proxies, modify settings or
// enumerate browser history/extensions. Loopback TCP checks send no payload.
func (p PathInspector) Inspect(ctx context.Context, paxInterface string) PathReport {
	report := PathReport{}
	add := func(kind, subject, state, value string) {
		if len(report.Findings) < 80 {
			report.Findings = append(report.Findings, PathFinding{kind, subject, state, value})
		} else {
			report.Partial = true
		}
	}
	raw, err := p.Command(ctx, "/usr/sbin/scutil", "--proxy")
	if err != nil || !strings.Contains(string(raw), "<dictionary>") {
		report.Partial = true
		add("system", "macOS", "unknown", "")
	} else {
		report.Findings = append(report.Findings, parseSystemProxies(string(raw))...)
		if strings.Contains(string(raw), "__SCOPED__") || strings.Contains(string(raw), "__SUPPLEMENTAL__") {
			report.Partial = true
			add("coverage", "scoped_proxy", "partial", "")
		}
	}
	dns, dnsErr := p.Command(ctx, "/usr/sbin/scutil", "--dns")
	dnsState := "unknown"
	if dnsErr == nil {
		dnsState = "other"
		for _, line := range strings.Split(string(dns), "\n") {
			key, value, ok := strings.Cut(strings.TrimSpace(line), " : ")
			if ok && strings.HasPrefix(key, "nameserver[") && (value == "172.29.231.2" || value == "fdfe:dcba:9876::2") {
				dnsState = "pax"
			}
		}
	}
	add("dns", "system", dnsState, "")
	// Socket ownership is a momentary observation. Only local proxy listeners
	// and browser-to-loopback established connections are retained.
	sockets, socketErr := p.Command(ctx, "/usr/sbin/netstat", "-anv", "-p", "tcp")
	if socketErr != nil {
		report.Partial = true
		add("sockets", "local", "unknown", "")
	}
	listeners := parseLocalSockets(string(sockets), add)
	for i := range report.Findings {
		finding := &report.Findings[i]
		if finding.Kind != "system" || finding.State != "enabled" {
			continue
		}
		host, _, splitErr := net.SplitHostPort(finding.Value)
		if splitErr != nil {
			continue
		}
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			continue
		}
		check, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		conn, dialErr := p.Dial(check, "tcp", finding.Value)
		cancel()
		if conn != nil {
			_ = conn.Close()
		}
		state := "reachable"
		if dialErr != nil {
			state = "unreachable"
			if ctx.Err() != nil {
				state = "unknown"
			}
		}
		add("proxy_port", finding.Subject, state, finding.Value)
		if owner := listeners[finding.Value]; owner != "" {
			add("listener", finding.Subject, "observed", owner)
		} else {
			add("listener", finding.Subject, "unknown", "")
		}
	}
	processes, processErr := p.Command(ctx, "/bin/ps", "-axo", "pid=,comm=")
	if processErr != nil {
		report.Partial = true
		add("browser", "processes", "unknown", "")
	} else {
		known := browserProcesses(string(processes))
		if len(known) == 0 {
			add("browser", "processes", "none_observed", "")
		} else {
			ids := make([]string, 0, len(known))
			for pid := range known {
				ids = append(ids, pid)
			}
			sort.Strings(ids)
			if len(ids) > 16 {
				ids = ids[:16]
				report.Partial = true
			}
			arguments, argsErr := p.Command(ctx, "/bin/ps", "-ww", "-p", strings.Join(ids, ","), "-o", "pid=,args=")
			if argsErr != nil {
				report.Partial = true
				add("browser", "processes", "unknown", "")
			} else {
				parseBrowserOverrides(string(arguments), known, add)
			}
		}
	}
	add("coverage", "browser_overrides", "partial", "extensions_policies_profiles_not_observed")
	targets := []string{"1.1.1.1", "2606:4700:4700::1111"}
	if p.Physical != nil {
		physical, err := p.Physical(ctx)
		state := physical.IPv6State
		if err != nil || state == "" {
			state = "unknown"
		}
		add("ip_family", "ipv6", state, physical.Name)
		if physical.IPv4Only() && err == nil {
			add("ip_family", "connection", "ipv4", "")
			targets = targets[:1]
		} else if state == "available" {
			add("ip_family", "connection", "dual", "")
		} else {
			add("ip_family", "connection", "unknown", "")
			report.Partial = true
		}
	}
	for _, target := range targets {
		args := []string{"-n", "get", target}
		if strings.Contains(target, ":") {
			args = []string{"-n", "get", "-inet6", target}
		}
		data, routeErr := p.Command(ctx, "/sbin/route", args...)
		iface := ParseRouteInterface(string(data))
		state := "other"
		if routeErr != nil || iface == "" {
			state = "unknown"
			report.Partial = true
		} else if paxInterface != "" && iface == paxInterface {
			state = "pax"
		}
		add("route", target, state, iface)
	}
	if ctx.Err() != nil {
		report.Partial = true
	}
	if after, afterErr := p.Command(ctx, "/usr/sbin/scutil", "--proxy"); afterErr != nil {
		report.Partial = true
	} else if string(raw) != string(after) {
		report.Changed = true
	}
	return report
}

func parseSystemProxies(raw string) []PathFinding {
	values := map[string]string{}
	// scutil's effective root dictionary is authoritative here. Scoped and
	// supplemental dictionaries are not flattened into global effective settings.
	for _, line := range strings.Split(raw, "\n") {
		if !strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "   ") {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimSpace(line), " : ")
		if ok {
			values[key] = value
		}
	}
	var result []PathFinding
	for _, kind := range []string{"HTTP", "HTTPS", "SOCKS"} {
		state := "disabled"
		endpoint := ""
		if values[kind+"Enable"] == "1" {
			state = "enabled"
			port, err := strconv.Atoi(values[kind+"Port"])
			host := values[kind+"Proxy"]
			if err != nil || port < 1 || port > 65535 || !safeProxyHost(host) {
				state = "unknown"
			} else {
				endpoint = net.JoinHostPort(host, strconv.Itoa(port))
			}
		}
		result = append(result, PathFinding{"system", kind, state, endpoint})
	}
	for _, kind := range []string{"ProxyAutoConfig", "ProxyAutoDiscovery"} {
		state := "disabled"
		if values[kind+"Enable"] == "1" {
			state = "dynamic"
		}
		result = append(result, PathFinding{"system", kind, state, ""})
	}
	return result
}

func browserName(command string) string {
	for _, known := range []string{"Google Chrome", "Microsoft Edge", "Brave Browser", "Chromium", "Firefox", "Safari", "ego", "Arc", "Vivaldi", "Opera"} {
		if strings.HasSuffix(command, "/"+known) || (known == "Firefox" && strings.HasSuffix(command, "/firefox")) {
			return known
		}
	}
	return ""
}

func browserProcesses(raw string) map[string]string {
	result := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if _, err := strconv.Atoi(fields[0]); err != nil {
			continue
		}
		if name := browserName(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), fields[0]))); name != "" {
			result[fields[0]] = name
		}
	}
	return result
}

func parseBrowserOverrides(raw string, known map[string]string, add func(string, string, string, string)) {
	seen := map[string]bool{}
	emit := func(subject, state, value string) {
		key := subject + "|" + state + "|" + value
		if !seen[key] {
			add("browser", subject, state, value)
			seen[key] = true
		}
	}
	for _, line := range strings.Split(raw, "\n") {
		arguments := strings.Fields(line)
		if len(arguments) < 2 {
			continue
		}
		name := known[arguments[0]]
		if name == "" {
			continue
		}
		found := false
		for i, arg := range arguments {
			key, value, hasValue := strings.Cut(arg, "=")
			switch key {
			case "--no-proxy-server":
				emit(name, "direct_override", "")
				found = true
			case "--proxy-auto-detect":
				emit(name, "dynamic_override", "")
				found = true
			case "--proxy-pac-url":
				emit(name, "dynamic_override", "")
				found = true
			case "--proxy-server":
				if !hasValue && i+1 < len(arguments) {
					value = arguments[i+1]
				}
				endpoint := safeProxyEndpoint(value)
				emit(name, "proxy_override", endpoint)
				found = true
			}
		}
		if !found {
			emit(name, "not_observed", "")
		}
	}
}

// Complex per-scheme rules are deliberately not guessed or displayed verbatim.
func safeProxyEndpoint(raw string) string {
	if strings.ContainsAny(raw, ";=\"' ") {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || !safeProxyHost(u.Hostname()) || u.Port() == "" {
		return ""
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return ""
	}
	return net.JoinHostPort(u.Hostname(), strconv.Itoa(port))
}

func safeProxyHost(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for _, ch := range host {
		if ch != '.' && ch != '-' && (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') {
			return false
		}
	}
	return true
}

func localEndpoint(raw string) (string, bool) {
	i := strings.LastIndex(raw, ".")
	if i < 0 {
		return "", false
	}
	host, port := raw[:i], raw[i+1:]
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", false
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", false
	}
	return net.JoinHostPort(ip.String(), port), true
}

func parseLocalSockets(raw string, add func(string, string, string, string)) map[string]string {
	listeners := map[string]string{}
	connections := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		f := strings.Fields(line)
		if len(f) < 12 || (f[0] != "tcp4" && f[0] != "tcp6") {
			continue
		}
		owner := ""
		for _, part := range f[10:] {
			if owner != "" {
				owner += " "
			}
			owner += part
			if strings.Contains(part, ":") {
				break
			}
		}
		colon := strings.LastIndex(owner, ":")
		if colon < 0 {
			continue
		}
		process := owner[:colon]
		if f[5] == "LISTEN" {
			if address, local := localEndpoint(f[3]); local {
				listeners[address] = safeProcess(process)
			}
			continue
		}
		if f[5] != "ESTABLISHED" {
			continue
		}
		browser := ""
		for _, candidate := range []string{"Chrome", "Safari", "firefox", "Brave", "Edge", "ego"} {
			if strings.Contains(strings.ToLower(process), strings.ToLower(candidate)) {
				browser = candidate
				break
			}
		}
		if browser == "" {
			continue
		}
		if endpoint, local := localEndpoint(f[4]); local {
			connections[browser+"|"+endpoint] = true
		}
	}
	keys := make([]string, 0, len(connections))
	for key := range connections {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts := strings.SplitN(key, "|", 2)
		add("browser_socket", parts[0], "observed", parts[1])
	}
	return listeners
}

func safeProcess(raw string) string {
	for _, name := range []string{"sing-box", "pax-connectd", "xray", "v2ray", "mihomo", "clash"} {
		if raw == name {
			return name
		}
	}
	return "other_process"
}
