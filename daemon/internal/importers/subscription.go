package importers

import (
	"encoding/json"
	"errors"
	"net/url"
	"pax-connect/daemon/internal/config"
	"strings"
	"unicode"
	"unicode/utf8"
)

type ParsedItem struct {
	Name     string
	Protocol string
	Line     int
	Route    *config.RouteConfig
	Error    string
}
type ParsedBatch struct{ Items []ParsedItem }

const maxSubscriptionItems = 1000

var (
	errImportTooLarge = errors.New("import_too_large")
	errImportEmpty    = errors.New("empty_import")
	errImportFormat   = errors.New("subscription_format_unsupported")
	errRemoteProvider = errors.New("subscription_provider_unsupported")
)

// ParseSubscription decodes inline Clash proxies, sing-box outbounds or a share-link list.
func ParseSubscription(body []byte) (ParsedBatch, error) {
	if len(body) > 2*1024*1024 {
		return ParsedBatch{}, errImportTooLarge
	}
	text := strings.TrimPrefix(string(body), "\ufeff")
	if format := subscriptionFormat(text); format != "" {
		return parseStructured(text, format)
	}
	if !strings.Contains(text, "://") {
		decoded, err := decodeBase64(text)
		if err != nil {
			return ParsedBatch{}, err
		}
		text = strings.TrimPrefix(string(decoded), "\ufeff")
		if format := subscriptionFormat(text); format != "" {
			return parseStructured(text, format)
		}
	}
	return parseLinks(text)
}

func parseLinks(text string) (ParsedBatch, error) {
	var batch ParsedBatch
	for index, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(batch.Items) >= maxSubscriptionItems {
			return ParsedBatch{}, errImportTooLarge
		}
		p, err := ParseLink(line)
		name, protocol := linkDisplay(line)
		item := ParsedItem{Line: index + 1, Name: name, Protocol: protocol}
		if err != nil {
			item.Error = err.Error()
		} else {
			item.Route = &p
		}
		batch.Items = append(batch.Items, item)
	}
	if len(batch.Items) == 0 {
		return batch, errImportEmpty
	}
	return batch, nil
}

// Detect mappings before looking for links. Ignored configuration fields may contain URLs.
func subscriptionFormat(text string) string {
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		switch line[0] {
		case '<', '[':
			return "unsupported"
		case '{':
			if json.Valid([]byte(text)) {
				return "json"
			}
			return "yaml"
		}
		colon := strings.IndexByte(line, ':')
		if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "%YAML") ||
			colon >= 0 && (colon == len(line)-1 || line[colon+1] == ' ' || line[colon+1] == '\t') {
			return "yaml"
		}
		break
	}
	return ""
}

func parseStructured(text, format string) (ParsedBatch, error) {
	if !utf8.ValidString(text) {
		return ParsedBatch{}, errImportFormat
	}
	switch format {
	case "json":
		return parseSingboxSubscription(text)
	case "yaml":
		return parseClashSubscription(text)
	default:
		return ParsedBatch{}, errImportFormat
	}
}

func itemDisplay(name, protocol string) (string, string) {
	if len(name) > 128 || strings.ContainsFunc(name, unicode.IsControl) {
		name = ""
	}
	if len(protocol) > 24 || strings.ContainsFunc(protocol, unicode.IsControl) {
		protocol = ""
	}
	return name, protocol
}

// Error rows expose only a bounded display name and protocol, never auth/server/query.
func linkDisplay(raw string) (string, string) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", ""
	}
	name := u.Fragment
	protocol := strings.ToLower(u.Scheme)
	if protocol == "vmess" {
		data, err := decodeBase64(strings.TrimPrefix(raw, "vmess://"))
		if err == nil && len(data) <= 32*1024 {
			var fields struct {
				Name string `json:"ps"`
			}
			if json.Unmarshal(data, &fields) == nil {
				name = fields.Name
			}
		}
	}
	return itemDisplay(name, protocol)
}
