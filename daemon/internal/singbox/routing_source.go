package singbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sagernet/sing-box/common/srs"
	"github.com/sagernet/sing-box/option"
	"pax-connect/daemon/internal/config"
)

// DecodeRoutingSource validates a bounded source/SRS without starting a kernel.
// The product stores private predicate JSON; no upstream types escape this adapter.
func DecodeRoutingSource(raw []byte) (result []config.RoutingEntry, revision string, failure error) {
	defer func() {
		if recover() != nil {
			result = nil
			revision = ""
			failure = config.ErrRoutingLibrary
		}
	}()

	if len(raw) == 0 || len(raw) > 2<<20 {
		return nil, "", config.ErrRoutingLibrary
	}
	var compat option.PlainRuleSetCompat
	var err error
	if bytes.HasPrefix(raw, []byte("SRS")) {
		if !boundedRoutingSRS(raw) {
			return nil, "", config.ErrRoutingLibrary
		}
		compat, err = srs.Read(bytes.NewReader(raw), true)
	} else {
		var document struct {
			Version int              `json:"version"`
			Rules   []map[string]any `json:"rules"`
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(&document) != nil {
			return nil, "", config.ErrRoutingLibrary
		}
		for _, predicate := range document.Rules {
			if !routingSourcePredicate(predicate, 0) {
				return nil, "", config.ErrRoutingLibrary
			}
		}
		err = json.Unmarshal(raw, &compat)
	}
	if err != nil {
		return nil, "", config.ErrRoutingLibrary
	}
	plain, err := compat.Upgrade()
	if err != nil || len(plain.Rules) > 10000 {
		return nil, "", config.ErrRoutingLibrary
	}
	hash := sha256.Sum256(raw)
	version := hex.EncodeToString(hash[:])[:12]
	out := make([]config.RoutingEntry, 0, len(plain.Rules))
	for _, rule := range plain.Rules {
		data, err := json.Marshal(rule)
		if err != nil {
			return nil, "", config.ErrRoutingLibrary
		}
		var decoded option.Rule
		if decoded.UnmarshalJSONContext(context.Background(), data) != nil {
			return nil, "", config.ErrRoutingLibrary
		}
		// Rule-set references and all action fields are forbidden in subscribed predicates.
		var object map[string]any
		if json.Unmarshal(data, &object) != nil || !routingSourcePredicate(object, 0) {
			return nil, "", config.ErrRoutingLibrary
		}
		id := sha256.Sum256(data)
		out = append(out, config.RoutingEntry{ID: hex.EncodeToString(id[:])[:24], Method: "source", Target: routingSourceTitle(object), Destination: "global", Enabled: true, Match: data})
	}
	return out, version, nil
}
func routingSourcePredicate(m map[string]any, depth int) bool {
	if depth > 16 {
		return false
	}
	for k, v := range m {
		switch k {
		case "type", "mode", "invert", "domain", "domain_suffix", "domain_keyword", "domain_regex", "ip_cidr", "source_ip_cidr", "port", "port_range", "source_port", "source_port_range", "process_name", "process_path", "process_path_regex", "network", "ip_version", "protocol":
		case "rules":
			list, ok := v.([]any)
			if !ok {
				return false
			}
			for _, child := range list {
				object, ok := child.(map[string]any)
				if !ok || !routingSourcePredicate(object, depth+1) {
					return false
				}
			}
		default:
			return false
		}
	}
	return len(m) > 0
}
func routingSourceTitle(m map[string]any) string {
	for _, item := range []struct{ key, title string }{{"domain", "域名"}, {"domain_suffix", "域名及子域"}, {"ip_cidr", "IP 网段"}, {"process_path", "应用路径"}, {"process_name", "进程"}} {
		if v, ok := m[item.key]; ok {
			text := fmt.Sprint(v)
			text = strings.Trim(text, "[]")
			if len([]rune(text)) > 180 {
				text = string([]rune(text)[:180]) + "…"
			}
			if len(m) > 1 {
				return item.title + "：" + text + " · 组合条件"
			}
			return item.title + "：" + text
		}
	}
	return fmt.Sprintf("复合匹配 · %d 项条件", len(m))
}
