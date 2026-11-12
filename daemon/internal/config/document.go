package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

const MaxDocumentBytes = 64 * 1024

// ParseDocument accepts one bounded private outbound, rejecting ambiguous keys and unsafe ownership.
// Kernel-specific unknown fields are rejected separately by the singbox adapter.
func ParseDocument(raw []byte) (map[string]any, error) {
	if len(raw) == 0 || len(raw) > MaxDocumentBytes || !utf8.Valid(raw) {
		return nil, ErrRouteDocument
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := documentValue(decoder, 0)
	if err != nil {
		return nil, ErrRouteDocument
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, ErrRouteDocument
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, ErrRouteDocument
	}
	if err = checkDocumentScope(object); err != nil {
		return nil, err
	}
	return object, nil
}
func documentValue(d *json.Decoder, depth int) (any, error) {
	if depth > 32 {
		return nil, errors.New("depth")
	}
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, container := token.(json.Delim)
	if !container {
		return token, nil
	}
	switch delim {
	case '{':
		object := map[string]any{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, errors.New("key")
			}
			if _, exists := object[name]; exists {
				return nil, errors.New("duplicate")
			}
			value, err := documentValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			object[name] = value
		}
		_, err = d.Token()
		return object, err
	case '[':
		values := []any{}
		for d.More() {
			value, err := documentValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		_, err = d.Token()
		return values, err
	default:
		return nil, errors.New("delimiter")
	}
}
func checkDocumentScope(object map[string]any) error {
	// Only the product-owned single outbound may be supplied; recursive path fields can read host secrets.
	forbidden := map[string]bool{"detour": true, "bind_interface": true, "inet4_bind_address": true, "inet6_bind_address": true, "routing_mark": true, "reuse_addr": true, "domain_resolver": true, "network_strategy": true, "network_type": true, "fallback_network_type": true, "certificate_path": true, "key_path": true, "certificate": true, "key": true, "client_certificate_path": true, "client_key_path": true, "client_certificate": true, "client_key": true, "plugin": true, "plugin_opts": true}
	var walk func(any) error
	walk = func(value any) error {
		switch v := value.(type) {
		case map[string]any:
			for key, child := range v {
				if forbidden[key] || strings.HasSuffix(key, "_path") && key != "ws_path" {
					return ErrDocumentField
				}
				if err := walk(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range v {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(object)
}
func stringField(m map[string]any, key string) string { value, _ := m[key].(string); return value }
func objectField(m map[string]any, key string) map[string]any {
	value, _ := m[key].(map[string]any)
	return value
}
func boolField(m map[string]any, key string) bool { value, _ := m[key].(bool); return value }

// RouteFromDocument derives the legacy typed projection; Document remains authoritative and private.
func RouteFromDocument(raw []byte, id, name string) (RouteConfig, error) {
	object, err := ParseDocument(raw)
	if err != nil {
		return RouteConfig{}, err
	}
	number, ok := object["server_port"].(json.Number)
	if !ok {
		return RouteConfig{}, ErrRouteDocument
	}
	port, err := number.Int64()
	if err != nil || port < 1 || port > 65535 {
		return RouteConfig{}, ErrRouteDocument
	}
	if name == "" {
		name = stringField(object, "tag")
	}
	if name == "" {
		name = stringField(object, "server")
	}
	p := RouteConfig{NodeProfile: NodeProfile{ID: id, Name: name, ProtocolName: stringField(object, "type"), Server: stringField(object, "server"), Port: uint16(port)}, SchemaVersion: 1, Method: stringField(object, "method"), UUID: stringField(object, "uuid"), Username: stringField(object, "username"), Password: stringField(object, "password"), VMessSecurity: stringField(object, "security"), Flow: stringField(object, "flow"), Security: "none", Transport: "tcp"}
	if p.ProtocolName == "hysteria2" {
		p.Transport = "quic"
	}
	if n, ok := object["alter_id"].(json.Number); ok {
		v, e := n.Int64()
		if e != nil || v < 0 || v > 65535 {
			return p, ErrRouteDocument
		}
		p.AlterID = int(v)
	}
	tls := objectField(object, "tls")
	if boolField(tls, "enabled") {
		p.Security = "tls"
		p.ServerName = stringField(tls, "server_name")
		p.TLSSkipSuggested = boolField(tls, "insecure")
		if values, ok := tls["alpn"].([]any); ok {
			for _, v := range values {
				s, ok := v.(string)
				if !ok {
					return p, ErrRouteDocument
				}
				p.ALPN = append(p.ALPN, s)
			}
		}
		p.Fingerprint = stringField(objectField(tls, "utls"), "fingerprint")
		reality := objectField(tls, "reality")
		if boolField(reality, "enabled") {
			p.Security = "reality"
			p.RealityPublicKey = stringField(reality, "public_key")
			p.RealityShortID = stringField(reality, "short_id")
		}
	}
	transport := objectField(object, "transport")
	if transport != nil {
		p.Transport = stringField(transport, "type")
		p.WSPath = stringField(transport, "path")
		p.WSHost = stringField(objectField(transport, "headers"), "Host")
		if p.Transport == "httpupgrade" {
			p.WSHost = stringField(transport, "host")
		}
	}
	p.ObfsPassword = stringField(objectField(object, "obfs"), "password")
	if p.ProtocolName == "socks" {
		version, valid := object["version"].(string)
		if _, exists := object["version"]; exists && !valid {
			return p, ErrDocumentField
		}
		if version != "" && version != "5" {
			return p, ErrDocumentField
		}
		object["version"] = "5"
	}
	if p.ProtocolName == "http" && p.Security == "tls" && p.ServerName == "" {
		if _, exists := tls["server_name"]; exists {
			if _, valid := tls["server_name"].(string); !valid {
				return p, ErrDocumentField
			}
		}
		p.ServerName = p.Server
		tls["server_name"] = p.Server
	}
	if err = ValidateRoute(p); err != nil {
		return p, err
	}
	object["tag"] = name
	p.Document, _ = json.Marshal(object)
	return p, nil
}

// DocumentForRoute converts schema1 fixed fields once, retaining supported optional fields on later edits.
func DocumentForRoute(p RouteConfig) json.RawMessage {
	if len(p.Document) > 0 {
		object, err := ParseDocument(p.Document)
		if err != nil {
			return bytes.Clone(p.Document)
		}
		object["tag"] = p.Name
		raw, _ := json.Marshal(object)
		return raw
	}
	object := map[string]any{"type": p.ProtocolName, "tag": p.Name, "server": p.Server, "server_port": p.Port}
	switch p.ProtocolName {
	case "socks", "http":
		if p.ProtocolName == "socks" {
			object["version"] = "5"
		}
		if p.Username != "" {
			object["username"] = p.Username
			object["password"] = p.Password
		}
	case "trojan", "anytls":
		object["password"] = p.Password
	case "shadowsocks":
		object["method"] = p.Method
		object["password"] = p.Password
	case "hysteria2":
		object["password"] = p.Password
		if p.ObfsPassword != "" {
			object["obfs"] = map[string]any{"type": "salamander", "password": p.ObfsPassword}
		}
	case "vmess":
		object["uuid"] = p.UUID
		object["alter_id"] = p.AlterID
		security := p.VMessSecurity
		if security == "" {
			security = "auto"
		}
		object["security"] = security
	case "vless":
		object["uuid"] = p.UUID
		if p.Flow != "" {
			object["flow"] = p.Flow
		}
	}
	if p.Security == "tls" || p.Security == "reality" {
		tls := map[string]any{"enabled": true, "server_name": p.ServerName}
		if p.TLSSkipSuggested {
			tls["insecure"] = true
		}
		if len(p.ALPN) > 0 {
			tls["alpn"] = p.ALPN
		}
		if p.Fingerprint != "" {
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": p.Fingerprint}
		}
		if p.Security == "reality" {
			tls["reality"] = map[string]any{"enabled": true, "public_key": p.RealityPublicKey, "short_id": p.RealityShortID}
		}
		object["tls"] = tls
	}
	if p.Transport == "httpupgrade" {
		object["transport"] = map[string]any{"type": "httpupgrade", "path": p.WSPath, "host": p.WSHost}
	}
	if p.Transport == "ws" {
		object["transport"] = map[string]any{"type": "ws", "path": p.WSPath, "headers": map[string]string{"Host": p.WSHost}}
	}
	raw, _ := json.Marshal(object)
	return raw
}
