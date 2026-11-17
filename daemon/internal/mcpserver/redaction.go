package mcpserver

import (
	"regexp"
	"strings"
)

var authURL = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s"<>]+`)
var authHeader = regexp.MustCompile(`(?i)(authorization|cookie)\s*[:=][^\r\n]+`)
var bearerCredential = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[a-z0-9._~+/=-]+`)
var secrets = regexp.MustCompile(`(?i)(authorization|cookie|api[_-]?key|password|passwd|token|secret|uuid)\s*[:=]\s*[^\s,;"}]+`)
var secretKey = regexp.MustCompile(`\b(?:sk-[a-zA-Z0-9_-]{8,}|sk-ant-[a-zA-Z0-9_-]+)\b`)
var credentialUUID = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
var codePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,79}$`)

func safeCode(code string) string {
	if codePattern.MatchString(code) {
		return code
	}
	return "internal_error"
}

func scrub(text string, identity bool) string {
	text = authURL.ReplaceAllStringFunc(text, func(url string) string {
		// Never echo userinfo, paths or queries from URLs in product/free text.
		scheme, rest, ok := strings.Cut(url, "://")
		if !ok {
			return "[redacted_url]"
		}
		if !strings.EqualFold(scheme, "http") && !strings.EqualFold(scheme, "https") {
			return "[redacted_link]"
		}
		host := strings.FieldsFunc(rest, func(r rune) bool { return r == '/' || r == '?' || r == '#' })
		if len(host) == 0 {
			return "[redacted_url]"
		}
		authority := host[0]
		if at := strings.LastIndex(authority, "@"); at >= 0 {
			authority = authority[at+1:]
		}
		return scheme + "://" + authority + "/[redacted]"
	})
	text = authHeader.ReplaceAllString(text, "[redacted_header]")
	text = bearerCredential.ReplaceAllString(text, "[redacted_credential]")
	text = secrets.ReplaceAllString(text, "[redacted_credential]")
	text = secretKey.ReplaceAllString(text, "[redacted_key]")
	if !identity {
		text = credentialUUID.ReplaceAllString(text, "[redacted_uuid]")
	}
	return text
}

func redact(value map[string]any) {
	for key, item := range value {
		lower := strings.ToLower(key)
		switch lower {
		case "raw", "documentjson", "password", "uuid", "apikey", "authorization", "cookie", "sourceurl":
			delete(value, key)
			continue
		}
		switch v := item.(type) {
		case string:
			if key == "code" || key == "errorCode" || key == "error_code" {
				if v != "" {
					value[key] = safeCode(v)
				}
				continue
			}
			identity := strings.HasSuffix(lower, "id") || strings.HasSuffix(lower, "ids") || strings.Contains(lower, "token") || strings.Contains(lower, "cursor") || lower == "daemon_instance" || lower == "daemoninstance"
			value[key] = scrub(v, identity)
		case map[string]any:
			redact(v)
		case []any:
			for i, element := range v {
				switch e := element.(type) {
				case map[string]any:
					redact(e)
				case string:
					v[i] = scrub(e, strings.HasSuffix(lower, "ids"))
				}
			}
		}
	}
}
