package singbox

import (
	"context"
	"encoding/json"

	"github.com/sagernet/sing-box/option"
	shadowsocks "github.com/sagernet/sing-shadowsocks2"
	"pax-connect/daemon/internal/config"
)

// ValidateRouteDocument uses the pinned kernel decoder without starting a kernel or touching network.
func ValidateRouteDocument(ctx context.Context, raw []byte) error {
	if _, err := config.ParseDocument(raw); err != nil {
		return err
	}
	var outbound option.Outbound
	if err := outbound.UnmarshalJSONContext(runtimeContext(ctx), raw); err != nil {
		return config.ErrDocumentField
	}
	if outbound.Type == "shadowsocks" {
		var fields struct {
			Method   string `json:"method"`
			Password string `json:"password"`
		}
		if json.Unmarshal(raw, &fields) != nil {
			return config.ErrRouteDocument
		}
		if _, err := shadowsocks.CreateMethod(ctx, fields.Method, shadowsocks.MethodOptions{Password: fields.Password}); err != nil {
			return config.ErrRouteDocument
		}
	}
	return nil
}
func documentOutbound(p config.RouteConfig, tag string) (map[string]any, error) {
	var object map[string]any
	if json.Unmarshal(p.Document, &object) != nil {
		return nil, ErrConfig
	}
	object["tag"] = tag
	object["domain_resolver"] = map[string]any{"server": "dns-local"}
	if tls, ok := object["tls"].(map[string]any); ok {
		tls["insecure"] = p.Security == "tls" && p.TLSResolved.Mode == config.TLSSkip
	}
	return object, nil
}
