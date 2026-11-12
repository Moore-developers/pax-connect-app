package singbox

import (
	"context"
	"github.com/sagernet/sing-box/adapter"
	boxdns "github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/service"
)

func runtimeContext(ctx context.Context) context.Context {
	ctx = include.Context(ctx)
	if registry, ok := service.FromContext[adapter.DNSTransportRegistry](ctx).(*boxdns.TransportRegistry); ok {
		boxdns.RegisterTransport[option.RemoteHTTPSDNSServerOptions](registry, "https", newEncryptedPool)
	}
	return service.ContextWithDefaultRegistry(ctx)
}
