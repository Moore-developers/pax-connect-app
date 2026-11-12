package singbox

// DefaultMixedAddress is the root core's product HTTP/SOCKS endpoint.
const DefaultMixedAddress = "127.0.0.1:17890"

func defaultMixedInbound() map[string]any {
	return map[string]any{"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": 17890}
}
