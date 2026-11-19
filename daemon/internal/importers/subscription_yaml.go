package importers

import (
	"encoding/json"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
	"pax-connect/daemon/internal/config"
)

func parseClashSubscription(text string) (ParsedBatch, error) {
	d := yaml.NewDecoder(strings.NewReader(text))
	var document, trailing yaml.Node
	if d.Decode(&document) != nil || len(document.Content) != 1 || d.Decode(&trailing) != io.EOF {
		return ParsedBatch{}, errImportFormat
	}
	fields, err := yamlMapping(document.Content[0])
	if err != nil {
		return ParsedBatch{}, errImportFormat
	}
	proxies, providers := fields["proxies"], fields["proxy-providers"]
	if providers != nil && (providers.Kind != yaml.MappingNode || providers.ShortTag() != "!!map") {
		return ParsedBatch{}, errImportFormat
	}
	hasProviders := providers != nil && len(providers.Content) > 0
	if proxies == nil {
		if hasProviders {
			return ParsedBatch{}, errRemoteProvider
		}
		return ParsedBatch{}, errImportFormat
	}
	if proxies.Kind != yaml.SequenceNode || proxies.ShortTag() != "!!seq" {
		return ParsedBatch{}, errImportFormat
	}
	if len(proxies.Content) == 0 {
		if hasProviders {
			return ParsedBatch{}, errRemoteProvider
		}
		return ParsedBatch{}, errImportEmpty
	}
	count := len(proxies.Content)
	if hasProviders {
		count++
	}
	if count > maxSubscriptionItems {
		return ParsedBatch{}, errImportTooLarge
	}
	batch := ParsedBatch{Items: make([]ParsedItem, 0, count)}
	for _, node := range proxies.Content {
		batch.Items = append(batch.Items, parseClashProxy(node))
	}
	if hasProviders {
		line := providers.Line
		for index, key := range document.Content[0].Content {
			if index%2 == 0 && key.Value == "proxy-providers" {
				line = key.Line
				break
			}
		}
		batch.Items = append(batch.Items, ParsedItem{Line: line, Error: errRemoteProvider.Error()})
	}
	return batch, nil
}

func parseClashProxy(node *yaml.Node) ParsedItem {
	item := ParsedItem{Line: node.Line}
	fields, err := yamlMapping(node)
	if err != nil {
		item.Error = err.Error()
		return item
	}
	name := yamlString(fields["name"])
	protocol := yamlString(fields["type"])
	item.Name, item.Protocol = itemDisplay(name, protocol)
	if item.Name == "" || item.Protocol == "" {
		item.Error = config.ErrRouteDocument.Error()
		return item
	}
	value, err := yamlJSONValue(node, 0)
	if err == nil {
		// Bound the node before conversion, including options omitted from the
		// normalized outbound (for example, an AnyTLS server's padding scheme).
		raw, _ := json.Marshal(value)
		if len(raw) > config.MaxDocumentBytes {
			err = errImportTooLarge
		} else {
			var route config.RouteConfig
			route, err = convertClashProxy(value.(map[string]any), name, protocol)
			if err == nil {
				item.Route = &route
				item.Protocol = route.ProtocolName
			}
		}
	}
	if err != nil {
		item.Error = err.Error()
	}
	return item
}

func yamlString(n *yaml.Node) string {
	if n != nil && n.Kind == yaml.ScalarNode && n.ShortTag() == "!!str" {
		return n.Value
	}
	return ""
}

func yamlMapping(n *yaml.Node) (map[string]*yaml.Node, error) {
	if n.Kind != yaml.MappingNode || n.ShortTag() != "!!map" {
		return nil, config.ErrRouteDocument
	}
	m := make(map[string]*yaml.Node, len(n.Content)/2)
	for i := 0; i < len(n.Content); i += 2 {
		key := yamlString(n.Content[i])
		if key == "" || m[key] != nil {
			return nil, config.ErrRouteDocument
		}
		m[key] = n.Content[i+1]
	}
	return m, nil
}

// Convert only in-scope node data. Validate tags and refuse aliases without ever
// expanding them, as a general YAML decode would do.
func yamlJSONValue(node *yaml.Node, depth int) (any, error) {
	if depth > 32 {
		return nil, config.ErrRouteDocument
	}
	switch node.Kind {
	case yaml.MappingNode:
		fields, err := yamlMapping(node)
		if err != nil {
			return nil, err
		}
		result := make(map[string]any, len(fields))
		for key, child := range fields {
			value, err := yamlJSONValue(child, depth+1)
			if err != nil {
				return nil, err
			}
			result[key] = value
		}
		return result, nil
	case yaml.SequenceNode:
		if node.ShortTag() != "!!seq" {
			return nil, config.ErrRouteDocument
		}
		result := make([]any, 0, len(node.Content))
		for _, child := range node.Content {
			value, err := yamlJSONValue(child, depth+1)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
		return result, nil
	case yaml.ScalarNode:
		switch node.ShortTag() {
		case "!!str":
			return node.Value, nil
		case "!!int":
			var value int64
			if node.Decode(&value) == nil {
				return value, nil
			}
		case "!!bool":
			var value bool
			if node.Decode(&value) == nil {
				return value, nil
			}
		case "!!null":
			return nil, nil
		}
	}
	return nil, config.ErrRouteDocument
}
