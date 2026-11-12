package telemetry

import (
	"pax-connect/daemon/internal/config"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var authorizationPattern = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*)(?:(?:bearer|basic)\s+)?[^\s,;]+`)

var credentialPattern = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*|(?:password|passwd|secret|token|private_key|uuid)\s*[:=]\s*)[^\s,;]+`)
var urlPattern = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s]+`)

// ScrubRaw retains diagnostic addresses, stripping credential fields and URL secrets.
// Configuration/certificate bodies are omitted rather than optimistically scrubbed.
func ScrubRaw(raw string) (string, bool, string) {
	lower := strings.ToLower(raw)
	// Configuration-shaped and unknown free text cannot be proven credential-free.
	if strings.ContainsAny(strings.ReplaceAll(raw, "[redacted]", "redacted"), "{}[]") || strings.Contains(lower, "bearer ") && !strings.Contains(lower, "authorization") {
		return "", false, "unsafe_format"
	}
	allowed := false
	for _, prefix := range []string{"connection", "outbound", "inbound", "router", "route", "dns", "dial ", "read ", "write ", "open ", "listen ", "lookup ", "exchange ", "context deadline", "i/o timeout", "tls", "quic", "authorization:", "password=", "password:", "secret=", "token=", "proxy:"} {
		if strings.HasPrefix(lower, prefix) {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", false, "unrecognized_format"
	}

	if strings.Contains(lower, "-----begin") || strings.Contains(raw, "\"uuid\"") || strings.Contains(raw, "\"private_key\"") || strings.Contains(raw, "\"outbounds\"") || strings.Contains(raw, "\"password\"") {
		return "", false, "unsafe_configuration"
	}
	raw = authorizationPattern.ReplaceAllString(raw, "$1[redacted]")
	raw = credentialPattern.ReplaceAllString(raw, "$1[redacted]")
	raw = urlPattern.ReplaceAllStringFunc(raw, func(v string) string {
		scheme, rest, ok := strings.Cut(v, "://")
		if !ok {
			return "[redacted]"
		}
		rest = strings.SplitN(rest, "?", 2)[0]
		rest = strings.SplitN(rest, "#", 2)[0]
		if n := strings.LastIndex(rest, "@"); n >= 0 {
			rest = rest[n+1:]
		}
		return scheme + "://" + rest
	})
	truncated := len(raw) > 4096
	if truncated {
		raw = raw[:4096]
		for !utf8.ValidString(raw) {
			raw = raw[:len(raw)-1]
		}
	}
	return raw, truncated, ""
}

// ProductEvent translates only explicit stages; a start never implies success.
func ProductEvent(o config.Observation) *config.HistoryEvent {
	if o.Kind == "sample" || o.Flow == nil && o.Kind != "diagnostic" || o.Kind == "diagnostic" && o.Level != "error" && o.Level != "warn" {
		return nil
	}
	titles := map[string]string{"started": "开始连接", "ready": "连接通道已建立", "closed": "连接已关闭", "failed": "连接失败", "rejected": "连接已阻断", "diagnostic": "内核诊断"}
	title := titles[o.Kind]
	if title == "" {
		return nil
	}
	if o.Flow != nil && config.IsDNSControl(*o.Flow) {
		switch o.Kind {
		case "started":
			title = "开始解析域名"
		case "ready":
			title = "域名解析成功"
		case "failed":
			title = "域名解析失败"
		case "closed":
			title = "DNS 查询完成"
		}
	}
	level := o.Level
	if level == "warn" {
		level = "warning"
	}
	if level == "" {
		level = "info"
	}
	if o.Kind == "failed" {
		level = "error"
	}
	e := &config.HistoryEvent{ID: o.Session + ":" + strconv.FormatUint(o.Sequence, 10), At: o.At, Kind: o.Kind, Title: title, Level: level, Source: "sing-box", Flow: o.Flow, Result: o.Code}
	if o.Raw != "" {
		raw, truncated, omitted := ScrubRaw(o.Raw)
		e.Truncated = truncated
		e.RawOmitted = omitted
		if raw != "" {
			e.Raw = []string{raw}
		}
	}
	return e
}

// ScrubEventText accepts product display strings, removing credentials and URL
// secrets without interpreting them as markup or a request to fetch anything.
func ScrubEventText(value string) string {
	lower := strings.ToLower(value)
	if strings.Contains(lower, "-----begin") {
		return "[已省略敏感内容]"
	}
	value = authorizationPattern.ReplaceAllString(value, "$1[redacted]")
	value = credentialPattern.ReplaceAllString(value, "$1[redacted]")
	return urlPattern.ReplaceAllStringFunc(value, func(s string) string {
		scheme, rest, ok := strings.Cut(s, "://")
		if !ok {
			return "[redacted]"
		}
		rest = strings.SplitN(strings.SplitN(rest, "?", 2)[0], "#", 2)[0]
		if i := strings.LastIndex(rest, "@"); i >= 0 {
			rest = rest[i+1:]
		}
		return scheme + "://" + rest
	})
}
