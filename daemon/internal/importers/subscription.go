package importers

import (
	"encoding/json"
	"errors"
	"net/url"
	"pax-connect/daemon/internal/config"
	"strings"
)

type ParsedItem struct {
	Name     string
	Protocol string
	Line     int
	Route    *config.RouteConfig
	Error    string
}
type ParsedBatch struct{ Items []ParsedItem }

// ParseSubscription decodes a bounded collection and always reuses ParseLink per item.
func ParseSubscription(body []byte) (ParsedBatch, error) {
	if len(body) > 2*1024*1024 {
		return ParsedBatch{}, errors.New("import_too_large")
	}
	text := strings.TrimSpace(strings.TrimPrefix(string(body), "\ufeff"))
	if structuredSubscription(text) {
		return ParsedBatch{}, errors.New("subscription_format_unsupported")
	}
	if !strings.Contains(text, "://") {
		decoded, err := decodeBase64(text)
		if err != nil {
			return ParsedBatch{}, err
		}
		text = strings.TrimSpace(string(decoded))
		if structuredSubscription(text) {
			return ParsedBatch{}, errors.New("subscription_format_unsupported")
		}
	}
	var batch ParsedBatch
	for index, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(batch.Items) >= 1000 {
			return ParsedBatch{}, errors.New("import_too_large")
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
		return batch, errors.New("empty_import")
	}
	return batch, nil
}

// Whole configuration documents are not share-link batches. Detect their shape
// before line parsing, including configs containing HTTP URLs in unrelated fields.
func structuredSubscription(text string) bool {
	if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") || strings.HasPrefix(text, "<") {
		return true
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "---" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "://") {
			return false
		}
		colon := strings.IndexByte(line, ':')
		if colon >= 0 && (colon == len(line)-1 || line[colon+1] == ' ' || line[colon+1] == '\t') {
			return true
		}
		return false
	}
	return false
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
	if len(name) > 128 {
		name = ""
	}
	if len(protocol) > 24 {
		protocol = ""
	}
	return name, protocol
}
