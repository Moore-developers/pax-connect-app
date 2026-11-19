package singbox

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/sagernet/sing-box/common/mux"
	boxtls "github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/option"
	shadowsocks "github.com/sagernet/sing-shadowsocks2"
	"github.com/sagernet/sing/common/logger"
	"pax-connect/daemon/internal/config"
)

// ValidateRouteDocument uses the pinned kernel decoder without starting a kernel or touching network.
func ValidateRouteDocument(ctx context.Context, raw []byte) error {
	document, outbound, err := decodeRouteDocument(ctx, raw)
	if err != nil {
		return err
	}
	if vless, ok := outbound.Options.(*option.VLESSOutboundOptions); ok && vless.Transport != nil && vless.Transport.Type == "grpc" {
		return validateRouteOptions(ctx, document, outbound)
	}
	return nil
}

// ValidateImportedRouteDocument also checks the fields admitted by the root core.
func ValidateImportedRouteDocument(ctx context.Context, raw []byte) error {
	document, outbound, err := decodeRouteDocument(ctx, raw)
	if err != nil {
		return err
	}
	return validateRouteOptions(ctx, document, outbound)
}

func validateRouteOptions(ctx context.Context, document map[string]any, outbound option.Outbound) error {
	typed := reflect.ValueOf(outbound)
	if !coreExactKeys(document, typed) || !coreAllowedValue(typed) {
		return config.ErrDocumentField
	}
	if anyTLS, ok := outbound.Options.(*option.AnyTLSOutboundOptions); ok && anyTLS.TCPFastOpen {
		return config.ErrDocumentField
	}
	var multiplex *option.OutboundMultiplexOptions
	switch options := outbound.Options.(type) {
	case *option.VLESSOutboundOptions:
		multiplex = options.Multiplex
	case *option.ShadowsocksOutboundOptions:
		if options.UDPOverTCP == nil || !options.UDPOverTCP.Enabled {
			multiplex = options.Multiplex
		}
	}
	if multiplex != nil && multiplex.Enabled {
		// Construction validates the active protocol and limits without dialing.
		client, err := mux.NewClientWithOptions(nil, logger.NOP(), *multiplex)
		if err != nil {
			return config.ErrDocumentField
		}
		_ = client.Close()
	}
	if withTLS, ok := outbound.Options.(option.OutboundTLSOptionsWrapper); ok {
		if settings := withTLS.TakeOutboundTLSOptions(); settings != nil && settings.Enabled {
			// The typed allowlist has already excluded filesystem-backed TLS options.
			server, _ := document["server"].(string)
			if _, err := boxtls.NewClient(ctx, logger.NOP(), server, *settings); err != nil {
				return config.ErrDocumentField
			}
		}
	}
	return nil
}

func decodeRouteDocument(ctx context.Context, raw []byte) (map[string]any, option.Outbound, error) {
	var outbound option.Outbound
	document, err := config.ParseDocument(raw)
	if err != nil {
		return nil, outbound, err
	}
	if err := outbound.UnmarshalJSONContext(runtimeContext(ctx), raw); err != nil {
		return nil, outbound, config.ErrDocumentField
	}
	if fields, ok := outbound.Options.(*option.ShadowsocksOutboundOptions); ok {
		if _, err := shadowsocks.CreateMethod(ctx, fields.Method, shadowsocks.MethodOptions{Password: fields.Password}); err != nil {
			return nil, outbound, config.ErrRouteDocument
		}
	}
	return document, outbound, nil
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
