package importers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"

	"pax-connect/daemon/internal/config"
)

type subscriptionJSONField struct {
	value json.RawMessage
	start int
}

// Read only the members of this object. RouteFromDocument checks nested node
// fields, while containers like dns and route never become product data.
func subscriptionJSONObject(text string) (map[string]subscriptionJSONField, error) {
	d := json.NewDecoder(strings.NewReader(text))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return nil, errImportFormat
	}
	fields := map[string]subscriptionJSONField{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return nil, errImportFormat
		}
		if _, duplicate := fields[key]; duplicate {
			return nil, errImportFormat
		}
		var value json.RawMessage
		if err = d.Decode(&value); err != nil {
			return nil, errImportFormat
		}
		fields[key] = subscriptionJSONField{value: value, start: int(d.InputOffset()) - len(value)}
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return nil, errImportFormat
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errImportFormat
	}
	return fields, nil
}

func parseSingboxSubscription(text string) (ParsedBatch, error) {
	fields, err := subscriptionJSONObject(text)
	if err != nil {
		return ParsedBatch{}, err
	}
	outbounds, hasOutbounds := fields["outbounds"]
	if !hasOutbounds {
		if _, clash := fields["proxies"]; clash {
			return parseClashSubscription(text)
		}
		if _, providers := fields["proxy-providers"]; providers {
			return parseClashSubscription(text)
		}
		if _, hasType := fields["type"]; !hasType {
			return ParsedBatch{}, errImportFormat
		}
		trimmed := strings.TrimLeft(text, " \t\r\n")
		item, skip := parseSingboxOutbound([]byte(trimmed), 1+strings.Count(text[:len(text)-len(trimmed)], "\n"))
		if skip {
			return ParsedBatch{}, errImportEmpty
		}
		return ParsedBatch{Items: []ParsedItem{item}}, nil
	}
	d := json.NewDecoder(bytes.NewReader(outbounds.value))
	if token, err := d.Token(); err != nil || token != json.Delim('[') {
		return ParsedBatch{}, errImportFormat
	}
	var batch ParsedBatch
	line := 1 + strings.Count(text[:outbounds.start], "\n")
	previous := 0
	for count := 0; d.More(); count++ {
		if count >= maxSubscriptionItems {
			return ParsedBatch{}, errImportTooLarge
		}
		var raw json.RawMessage
		if err = d.Decode(&raw); err != nil {
			return ParsedBatch{}, errImportFormat
		}
		start := int(d.InputOffset()) - len(raw)
		line += bytes.Count(outbounds.value[previous:start], []byte{'\n'})
		previous = start
		item, skip := parseSingboxOutbound(raw, line)
		if !skip {
			batch.Items = append(batch.Items, item)
		}
	}
	if len(batch.Items) == 0 {
		return ParsedBatch{}, errImportEmpty
	}
	return batch, nil
}

func parseSingboxOutbound(raw []byte, line int) (ParsedItem, bool) {
	item := ParsedItem{Line: line}
	if len(raw) > config.MaxDocumentBytes {
		item.Error = errImportTooLarge.Error()
		return item, false
	}
	fields, err := subscriptionJSONObject(string(raw))
	if err != nil {
		item.Error = config.ErrRouteDocument.Error()
		return item, false
	}
	var name, protocol string
	if tag, ok := fields["tag"]; ok {
		if len(tag.value) == 0 || tag.value[0] != '"' || json.Unmarshal(tag.value, &name) != nil {
			item.Error = config.ErrRouteDocument.Error()
			return item, false
		}
	}
	if json.Unmarshal(fields["type"].value, &protocol) != nil || protocol == "" {
		item.Error = config.ErrRouteDocument.Error()
		return item, false
	}
	item.Name, item.Protocol = itemDisplay(name, protocol)
	switch protocol {
	case "direct", "block", "dns", "selector", "urltest":
		return item, true
	case "vless", "anytls", "hysteria2", "shadowsocks":
	default:
		item.Error = "unsupported_protocol"
		return item, false
	}
	object, err := config.ParseDocument(raw)
	if err == nil && name != "" && item.Name == "" {
		err = config.ErrRouteDocument
	}
	if err == nil {
		err = normalizeSingboxOutbound(object, protocol)
	}
	if err == nil {
		var route config.RouteConfig
		route, err = routeFromObject(object)
		if err == nil {
			item.Route = &route
			item.Name = route.Name
		}
	}
	if err != nil {
		item.Error = err.Error()
	}
	return item, false
}

func normalizeSingboxOutbound(out map[string]any, protocol string) error {
	if network, present := out["network"]; present {
		mode, err := normalizedNetwork(network)
		if err != nil {
			return err
		}
		if mode == "" {
			delete(out, "network")
		} else {
			out["network"] = mode
		}
	}
	if tls, ok := out["tls"].(map[string]any); ok && tls["enabled"] == true {
		name, exists := tls["server_name"]
		if !exists || name == "" {
			tls["server_name"] = out["server"]
		}
		if alpn, ok := tls["alpn"].(string); ok {
			tls["alpn"] = []string{alpn}
		}
	}
	switch protocol {
	case "vless":
		if value, ok := out["packet_encoding"]; ok && value != nil &&
			value != "" && value != "xudp" && value != "packetaddr" {
			return config.ErrDocumentField
		}
		if transport, ok := out["transport"].(map[string]any); ok {
			if transport["type"] == "xhttp" || transport["type"] == "splithttp" {
				return errors.New("unsupported_transport_xhttp")
			}
			if path, ok := transport["path"].(string); ok {
				if _, err := url.Parse(path); err != nil {
					return config.ErrRouteDocument
				}
			}
		}
	case "anytls":
		if padding, exists := out["padding_scheme"]; exists {
			switch padding := padding.(type) {
			case string:
			case []any:
				if !isStringList(padding) {
					return config.ErrRouteDocument
				}
			default:
				return config.ErrRouteDocument
			}
			// Padding is set by the AnyTLS server and can be updated in-session.
			delete(out, "padding_scheme")
		}
	case "hysteria2":
		for _, field := range []string{"server_ports", "hop_interval", "hop_interval_max"} {
			if _, exists := out[field]; exists {
				return config.ErrDocumentField
			}
		}
		if obfs, ok := out["obfs"].(map[string]any); ok {
			if obfs["type"] != "salamander" {
				return config.ErrDocumentField
			}
			password, ok := obfs["password"].(string)
			if !ok || password == "" {
				return config.ErrRouteDocument
			}
		}
	}
	return nil
}

func normalizedNetwork(value any) (string, error) {
	switch network := value.(type) {
	case nil:
		return "", nil
	case string:
		if network == "tcp" || network == "udp" {
			return network, nil
		}
	case []any:
		switch len(network) {
		case 0:
			return "", nil
		case 1:
			if entry, ok := network[0].(string); ok {
				return normalizedNetwork(entry)
			}
		case 2:
			if network[0] == "tcp" && network[1] == "udp" || network[0] == "udp" && network[1] == "tcp" {
				return "", nil
			}
		}
	}
	return "", config.ErrDocumentField
}

func routeFromObject(out map[string]any) (config.RouteConfig, error) {
	raw, err := json.Marshal(out)
	if err != nil {
		return config.RouteConfig{}, config.ErrRouteDocument
	}
	route, err := config.RouteFromDocument(raw, "", "")
	if err == nil {
		if name, _ := itemDisplay(route.Name, ""); name == "" {
			return config.RouteConfig{}, config.ErrRouteDocument
		}
	}
	return route, err
}

func isStringList(list []any) bool {
	for _, entry := range list {
		if _, ok := entry.(string); !ok {
			return false
		}
	}
	return true
}
