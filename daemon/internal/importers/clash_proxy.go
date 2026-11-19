package importers

import (
	"errors"
	"net"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"

	"pax-connect/daemon/internal/config"
)

func convertClashProxy(fields map[string]any, name, protocol string) (config.RouteConfig, error) {
	var zero config.RouteConfig
	if protocol == "ss" {
		protocol = "shadowsocks"
	}
	out := map[string]any{"type": protocol, "tag": name, "server": fields["server"], "server_port": fields["port"]}
	var err error
	switch protocol {
	case "vless":
		err = convertClashVLESS(out, fields)
	case "anytls":
		err = convertClashAnyTLS(out, fields)
	case "hysteria2":
		err = convertClashHY2(out, fields)
	case "shadowsocks":
		err = checkClashFields(fields, "password cipher udp")
		out["password"], out["method"] = fields["password"], fields["cipher"]
	default:
		return zero, errors.New("unsupported_protocol")
	}
	if err != nil {
		return zero, err
	}
	if protocol != "hysteria2" {
		udp, err := optionalClashField[bool](fields, "udp")
		if err != nil {
			return zero, err
		}
		if !udp {
			out["network"] = "tcp"
		}
	}
	return routeFromObject(out)
}

func checkClashFields(fields map[string]any, allowed string) error {
	return checkClashKeys(fields, "name type server port "+allowed)
}

func checkClashKeys(fields map[string]any, allowed string) error {
	names := " " + allowed + " "
	for key := range fields {
		if strings.Contains(key, " ") || !strings.Contains(names, " "+key+" ") {
			return config.ErrDocumentField
		}
	}
	return nil
}

func optionalClashField[T any](fields map[string]any, key string) (T, error) {
	var zero T
	value, exists := fields[key]
	if !exists {
		return zero, nil
	}
	typed, ok := value.(T)
	if !ok {
		return zero, config.ErrRouteDocument
	}
	return typed, nil
}

func clashTLS(fields map[string]any, sniField, fallback string) (map[string]any, error) {
	sni, err := optionalClashField[string](fields, sniField)
	if err != nil {
		return nil, err
	}
	if sni == "" {
		sni = fallback
	}
	tls := map[string]any{"enabled": true, "server_name": sni}
	if _, present := fields["skip-cert-verify"]; present {
		insecure, err := optionalClashField[bool](fields, "skip-cert-verify")
		if err != nil {
			return nil, err
		}
		tls["insecure"] = insecure
	}
	if alpn, present := fields["alpn"]; present {
		values, ok := alpn.([]any)
		if !ok || !isStringList(values) {
			return nil, config.ErrRouteDocument
		}
		tls["alpn"] = values
	}
	fingerprint, err := optionalClashField[string](fields, "client-fingerprint")
	if err != nil {
		return nil, err
	}
	if fingerprint != "" {
		tls["utls"] = map[string]any{"enabled": true, "fingerprint": fingerprint}
	}
	return tls, nil
}

func convertClashAnyTLS(out, fields map[string]any) error {
	if err := checkClashFields(fields, "password sni skip-cert-verify alpn client-fingerprint udp padding-scheme"); err != nil {
		return err
	}
	// The AnyTLS client uses its default and then accepts server padding updates.
	// The exporter may include the server's string in the node.
	if _, err := optionalClashField[string](fields, "padding-scheme"); err != nil {
		return err
	}
	server, _ := fields["server"].(string)
	tls, err := clashTLS(fields, "sni", server)
	if err != nil {
		return err
	}
	out["password"], out["tls"] = fields["password"], tls
	return nil
}

func convertClashHY2(out, fields map[string]any) error {
	if err := checkClashFields(fields, "password sni skip-cert-verify alpn obfs obfs-password"); err != nil {
		return err
	}
	server, _ := fields["server"].(string)
	tls, err := clashTLS(fields, "sni", server)
	if err != nil {
		return err
	}
	out["password"], out["tls"] = fields["password"], tls
	obfs, err := optionalClashField[string](fields, "obfs")
	if err != nil {
		return err
	}
	password, err := optionalClashField[string](fields, "obfs-password")
	if err != nil {
		return err
	}
	switch obfs {
	case "":
		if password != "" {
			return config.ErrRouteDocument
		}
	case "salamander":
		if password == "" {
			return config.ErrRouteDocument
		}
		out["obfs"] = map[string]any{"type": obfs, "password": password}
	default:
		return config.ErrDocumentField
	}
	return nil
}

func convertClashVLESS(out, fields map[string]any) error {
	network, err := optionalClashField[string](fields, "network")
	if err != nil {
		return err
	}
	switch network {
	case "xhttp", "splithttp":
		return errors.New("unsupported_transport_xhttp")
	case "", "tcp", "ws", "grpc":
	default:
		return errors.New("unsupported_transport")
	}
	allowed := "uuid tls servername skip-cert-verify alpn client-fingerprint network flow reality-opts ws-opts udp packet-encoding xudp packet-addr encryption"
	if network == "grpc" {
		allowed += " grpc-opts"
	}
	if err = checkClashFields(fields, allowed); err != nil {
		return err
	}
	encryption, err := optionalClashField[string](fields, "encryption")
	if err != nil {
		return err
	}
	if encryption != "" && encryption != "none" {
		return config.ErrDocumentField
	}
	enabled, err := optionalClashField[bool](fields, "tls")
	if err != nil || !enabled {
		return config.ErrRouteDocument
	}
	server, _ := fields["server"].(string)
	identity := server
	if network == "ws" {
		transport, host, err := clashWebsocket(fields)
		if err != nil {
			return err
		}
		if host != "" {
			identity = host
		} else if transport["type"] == "httpupgrade" {
			port, _ := fields["port"].(int64)
			transport["host"] = net.JoinHostPort(server, strconv.FormatInt(port, 10))
		}
		out["transport"] = transport
	} else if _, present := fields["ws-opts"]; present {
		return config.ErrDocumentField
	} else if network == "grpc" {
		transport, err := clashGRPC(fields)
		if err != nil {
			return err
		}
		out["transport"] = transport
	}
	tls, err := clashTLS(fields, "servername", identity)
	if err != nil {
		return err
	}
	if network == "ws" {
		if alpn, ok := tls["alpn"].([]any); ok && len(alpn) != 0 && (len(alpn) != 1 || alpn[0] != "http/1.1") {
			return config.ErrDocumentField
		}
	}
	if reality, err := optionalClashField[map[string]any](fields, "reality-opts"); err != nil {
		return err
	} else if reality != nil {
		if err := checkClashKeys(reality, "public-key short-id"); err != nil {
			return err
		}
		publicKey, err := optionalClashField[string](reality, "public-key")
		if err != nil {
			return err
		}
		shortID, err := optionalClashField[string](reality, "short-id")
		if err != nil {
			return err
		}
		tls["reality"] = map[string]any{"enabled": true, "public_key": publicKey, "short_id": shortID}
	}
	flow, err := optionalClashField[string](fields, "flow")
	if err != nil {
		return err
	}
	out["uuid"], out["tls"] = fields["uuid"], tls
	if flow != "" {
		out["flow"] = flow
	}
	return clashPacketEncoding(out, fields)
}

func clashGRPC(fields map[string]any) (map[string]any, error) {
	options, err := optionalClashField[map[string]any](fields, "grpc-opts")
	if err != nil {
		return nil, err
	}
	if err = checkClashKeys(options, "grpc-service-name"); err != nil {
		return nil, err
	}
	serviceName, err := optionalClashField[string](options, "grpc-service-name")
	if err != nil {
		return nil, err
	}
	if serviceName == "" {
		serviceName = "GunService"
	}
	return map[string]any{"type": "grpc", "service_name": serviceName}, nil
}

func clashWebsocket(fields map[string]any) (map[string]any, string, error) {
	ws, err := optionalClashField[map[string]any](fields, "ws-opts")
	if err != nil {
		return nil, "", err
	}
	if err = checkClashKeys(ws, "path headers v2ray-http-upgrade"); err != nil {
		return nil, "", err
	}
	path, err := optionalClashField[string](ws, "path")
	if err != nil {
		return nil, "", err
	}
	uri, err := url.Parse(path)
	if err != nil {
		return nil, "", config.ErrRouteDocument
	}
	// Mihomo expands ?ed= into early data. Passing it through as a query would
	// silently have a different effect in the pinned sing-box.
	if uri.Query().Has("ed") {
		return nil, "", config.ErrDocumentField
	}
	transport := map[string]any{"type": "ws", "path": path}
	upgrade, err := optionalClashField[bool](ws, "v2ray-http-upgrade")
	if err != nil {
		return nil, "", err
	}
	headers, err := optionalClashField[map[string]any](ws, "headers")
	if err != nil {
		return nil, "", err
	}
	normalized := map[string]string{}
	for key, value := range headers {
		name := textproto.CanonicalMIMEHeaderKey(key)
		header, ok := value.(string)
		if _, duplicate := normalized[name]; duplicate || !ok || strings.ContainsAny(header, "\r\n") {
			return nil, "", config.ErrRouteDocument
		}
		normalized[name] = header
	}
	host := normalized["Host"]
	if upgrade {
		transport["type"] = "httpupgrade"
		transport["host"] = host
		delete(normalized, "Host")
	}
	if len(normalized) > 0 {
		transport["headers"] = normalized
	}
	return transport, host, nil
}

func clashPacketEncoding(out, fields map[string]any) error {
	encoding, err := optionalClashField[string](fields, "packet-encoding")
	if err != nil {
		return err
	}
	packetAddr, err := optionalClashField[bool](fields, "packet-addr")
	if err != nil {
		return err
	}
	xudp, err := optionalClashField[bool](fields, "xudp")
	if err != nil {
		return err
	}
	switch encoding {
	case "packet", "packetaddr":
		out["packet_encoding"] = "packetaddr"
	case "", "xudp":
		// Mihomo enables XUDP by default unless packet-addr was requested.
		if packetAddr && !xudp {
			out["packet_encoding"] = "packetaddr"
		} else if encoding != "" {
			out["packet_encoding"] = encoding
		}
	default:
		return config.ErrDocumentField
	}
	return nil
}
