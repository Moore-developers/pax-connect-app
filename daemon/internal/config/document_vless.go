package config

// VLESS gRPC uses the pinned kernel's lite transport and its fixed Tun method.
func normalizeVLESSGRPC(object, transport, tls map[string]any) error {
	for key := range transport {
		if key != "type" && key != "service_name" {
			return ErrDocumentField
		}
	}
	serviceName, valid := transport["service_name"].(string)
	if _, exists := transport["service_name"]; exists && !valid {
		return ErrRouteDocument
	}
	transport["service_name"] = serviceName

	if flow, exists := object["flow"]; exists {
		if _, valid := flow.(string); !valid {
			return ErrRouteDocument
		}
	}
	if name, exists := tls["server_name"]; boolField(tls, "enabled") && (!exists || name == "") {
		tls["server_name"] = object["server"]
	}
	switch alpn := tls["alpn"].(type) {
	case string:
		tls["alpn"] = []any{alpn}
	case nil:
		delete(tls, "alpn")
	case []any:
		if len(alpn) == 0 {
			delete(tls, "alpn")
		}
	default:
		return ErrRouteDocument
	}

	if boolField(objectField(tls, "reality"), "enabled") {
		if engine := stringField(tls, "engine"); engine != "" && engine != "go" {
			return ErrDocumentField
		}
		if !boolField(objectField(tls, "utls"), "enabled") {
			return ErrRouteDocument
		}
	}
	return nil
}
