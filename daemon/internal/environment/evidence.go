package environment

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pax-connect/daemon/internal/measurement"
	"pax-connect/daemon/internal/network"
)

// Check is bounded evidence for one fixed environment target and stage.
type Check struct {
	Kind       string    `json:"kind"`
	Target     string    `json:"target"`
	State      string    `json:"state"`
	Value      string    `json:"value"`
	ErrorCode  string    `json:"errorCode"`
	DurationMS uint64    `json:"durationMS"`
	HTTPStatus uint32    `json:"httpStatus"`
	At         time.Time `json:"at"`
}

func probeError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "probe_cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "probe_timeout"
	}
	code := err.Error()
	if strings.HasPrefix(code, "probe_http_") || code == "probe_response_too_large" || code == "dns_session_invalid" || code == "dns_response_invalid" || code == "dns_no_results" {
		return code
	}
	return measurement.Classify(err, false).Code
}

func ipCheck(ctx context.Context, client measurement.Client, v6 bool) (string, Check) {
	kind, host := "ipv4", "ipv4.icanhazip.com"
	if v6 {
		kind, host = "ipv6", "ipv6.icanhazip.com"
	}
	check := Check{Kind: kind, Target: host, At: time.Now(), State: "failed"}
	started := time.Now()
	body, err := readBody(ctx, client, "https://"+host)
	check.DurationMS = uint64(time.Since(started).Milliseconds())
	if err != nil {
		check.ErrorCode = probeError(err)
		return "", check
	}
	ip := publicIP(strings.TrimSpace(string(body)))
	if ip == "" || strings.Contains(ip, ":") != v6 {
		check.ErrorCode = "probe_address_invalid"
		return "", check
	}
	check.State = "observed"
	check.Value = ip
	check.HTTPStatus = http.StatusOK
	return ip, check
}

func ipv6RouteCheck(ctx context.Context) Check {
	check := Check{Kind: "ipv6_route", Target: "macOS default route", At: time.Now(), State: "unknown"}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	started := time.Now()
	body, err := network.NewPathInspector().Command(bounded, "/sbin/route", "-n", "get", "-inet6", "default")
	check.DurationMS = uint64(time.Since(started).Milliseconds())
	if errors.Is(err, network.ErrNoRoute) {
		check.State = "absent"
		check.ErrorCode = "ipv6_no_default_route"
	} else if err != nil {
		check.ErrorCode = "ipv6_route_unavailable"
	} else if network.ParseRouteInterface(string(body)) != "" {
		check.State = "observed"
		check.Value = network.ParseRouteInterface(string(body))
	}
	return check
}

func dnsResultCheck(ctx context.Context, client measurement.Client, id string) ([]Resolver, Check) {
	check := Check{Kind: "dns_result", Target: "bash.ws", At: time.Now(), State: "failed"}
	started := time.Now()
	resolvers, err := dnsResults(ctx, client, id)
	check.DurationMS = uint64(time.Since(started).Milliseconds())
	check.ErrorCode = probeError(err)
	if err == nil {
		check.State = "observed"
		check.Value = strconv.Itoa(len(resolvers)) + " resolvers"
		check.HTTPStatus = 200
	}
	return resolvers, check
}

var browserTargets = map[string]string{
	"ipv4": "ipv4.icanhazip.com", "ipv6": "ipv6.icanhazip.com",
	"dns_session": "bash.ws", "dns_query": "bash.ws",
	"rtc_cf": "stun.cloudflare.com:3478", "rtc_google": "stun.l.google.com:19302",
	"echo_cf": "www.cloudflare.com", "echo_ipinfo": "ipinfo.io", "echo_ipip": "myip.ipip.net",
}

func validateChecks(checks []Check) error {
	if len(checks) > 12 {
		return errors.New("sample_checks_limit")
	}
	seen := map[string]bool{}
	for _, check := range checks {
		limit := 240
		if strings.HasPrefix(check.Kind, "rtc_") {
			limit = 4096
		}
		if browserTargets[check.Kind] == "" || check.Target != browserTargets[check.Kind] || seen[check.Kind] || len(check.Value) > limit || len(check.ErrorCode) > 80 || check.DurationMS > 45000 || check.HTTPStatus > 599 || check.At.IsZero() || time.Since(check.At) > 2*time.Minute || time.Until(check.At) > 2*time.Minute {
			return errors.New("sample_check_invalid")
		}
		seen[check.Kind] = true
		if strings.ContainsAny(check.Value+check.ErrorCode, "\x00\r\n") {
			return errors.New("sample_check_text_invalid")
		}
		switch check.State {
		case "observed", "failed", "unsupported", "empty", "timeout", "attempted":
		default:
			return errors.New("sample_check_state_invalid")
		}
		allowed := check.ErrorCode == "" || check.ErrorCode == "probe_timeout" || check.ErrorCode == "browser_request_failed" || check.ErrorCode == "probe_response_invalid" || check.ErrorCode == "rtc_api_unavailable" || check.ErrorCode == "rtc_operation_failed" || check.ErrorCode == "rtc_gathering_timeout" || check.ErrorCode == "dns_session_invalid"
		if strings.HasPrefix(check.ErrorCode, "probe_http_") {
			n, err := strconv.Atoi(strings.TrimPrefix(check.ErrorCode, "probe_http_"))
			allowed = err == nil && n >= 100 && n <= 599
		}
		if strings.HasPrefix(check.ErrorCode, "rtc_server_") {
			n, err := strconv.Atoi(strings.TrimPrefix(check.ErrorCode, "rtc_server_"))
			allowed = err == nil && n >= 300 && n <= 799
		}
		for _, code := range []string{"target_dns_failed", "target_tls_failed", "target_network_unreachable", "target_connection_refused", "target_connection_reset", "target_connection_closed", "target_connection_timeout", "target_connection_failed", "probe_response_too_large"} {
			if check.ErrorCode == code {
				allowed = true
			}
		}
		if check.State == "observed" && (check.Kind == "ipv4" || check.Kind == "ipv6" || strings.HasPrefix(check.Kind, "echo_")) {
			parts := strings.Split(check.Value, " · ")
			if publicIP(parts[0]) == "" || len(parts) > 2 {
				return errors.New("sample_check_address_invalid")
			}
			if len(parts) == 2 && (check.Kind != "echo_cf" || len(parts[1]) != 3 || strings.Trim(parts[1], "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "") {
				return errors.New("sample_check_colo_invalid")
			}
		}
		if !allowed {
			return errors.New("sample_check_code_invalid")
		}
	}
	return nil
}
