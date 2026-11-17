package mcpclientconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

var errInvalid = errors.New("configuration_invalid")

type document struct {
	format string
	object map[string]any
	yaml   yaml.Node
}
type entry struct {
	name     string
	config   map[string]any
	node     *yaml.Node
	disabled bool
}

func parse(raw []byte, format string) (*document, error) {
	d := &document{format: format, object: map[string]any{}}
	if len(bytes.TrimSpace(raw)) == 0 {
		return d, nil
	}
	var err error
	switch format {
	case "toml":
		err = toml.Unmarshal(raw, &d.object)
	case "json":
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var v any
		v, err = jsonValue(dec, 0)
		if err == nil {
			var ok bool
			d.object, ok = v.(map[string]any)
			if !ok {
				err = errInvalid
			}
			if _, e := dec.Token(); e != io.EOF {
				err = errInvalid
			}
		}
	case "yaml":
		dec := yaml.NewDecoder(bytes.NewReader(raw))
		err = dec.Decode(&d.yaml)
		if err == nil {
			var rest yaml.Node
			if dec.Decode(&rest) != io.EOF {
				err = errInvalid
			}
		}
		if err == nil && len(d.yaml.Content) == 1 {
			if !validYAML(d.yaml.Content[0], 0) {
				err = errInvalid
			}
		} else {
			err = errInvalid
		}
	default:
		err = errInvalid
	}
	if err != nil {
		return nil, errInvalid
	}
	return d, nil
}

func jsonValue(d *json.Decoder, depth int) (any, error) {
	if depth > 32 {
		return nil, errInvalid
	}
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := token.(json.Delim); ok {
		switch delim {
		case '{':
			m := map[string]any{}
			for d.More() {
				key, e := d.Token()
				if e != nil {
					return nil, e
				}
				s, ok := key.(string)
				if !ok {
					return nil, errInvalid
				}
				if _, ok = m[s]; ok {
					return nil, errInvalid
				}
				v, e := jsonValue(d, depth+1)
				if e != nil {
					return nil, e
				}
				m[s] = v
			}
			_, err = d.Token()
			return m, err
		case '[':
			a := []any{}
			for d.More() {
				v, e := jsonValue(d, depth+1)
				if e != nil {
					return nil, e
				}
				a = append(a, v)
			}
			_, err = d.Token()
			return a, err
		}
		return nil, errInvalid
	}
	return token, nil
}
func validYAML(n *yaml.Node, depth int) bool {
	if depth > 32 || n.Kind == yaml.AliasNode || n.Anchor != "" {
		return false
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Tag != "!!str" || seen[k.Value] {
				return false
			}
			seen[k.Value] = true
		}
	}
	for _, c := range n.Content {
		if !validYAML(c, depth+1) {
			return false
		}
	}
	return true
}
func field(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}
func scalar(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	return n.Value
}
func candidate(name string, c map[string]any) bool {
	command, _ := c["command"].(string)
	return name == "pax" || name == "pax-mcp" || strings.HasSuffix(command, "/pax-mcp")
}
func (d *document) entries() ([]entry, error) {
	out := []entry{}
	if d.format != "yaml" {
		key := "mcp_servers"
		if d.format == "json" {
			key = "mcpServers"
		}
		if d.object[key] == nil {
			return out, nil
		}
		m, ok := d.object[key].(map[string]any)
		if !ok {
			return nil, errInvalid
		}
		for name, v := range m {
			c, ok := v.(map[string]any)
			if !ok {
				return nil, errInvalid
			}
			if candidate(name, c) {
				out = append(out, entry{name: name, config: c})
			}
		}
		return out, nil
	}
	if len(d.yaml.Content) == 0 {
		return out, nil
	}
	root := d.yaml.Content[0]
	if root.Kind != yaml.SequenceNode {
		return nil, errInvalid
	}
	for _, patch := range root.Content {
		if patch.Kind != yaml.MappingNode {
			return nil, errInvalid
		}
		insert := field(patch, "insert")
		if insert == nil {
			continue
		}
		if insert.Kind != yaml.SequenceNode {
			return nil, errInvalid
		}
		for _, plugin := range insert.Content {
			name := scalar(field(plugin, "name"))
			id := scalar(field(plugin, "id"))
			conf := field(plugin, "config")
			if name != "@deepseek-ai/dsh-mcp-client" {
				if id == "pax-mcp" {
					return nil, errInvalid
				}
				continue
			}
			c := map[string]any{}
			if conf == nil || conf.Decode(&c) != nil {
				return nil, errInvalid
			}
			server, _ := c["serverName"].(string)
			if id == "pax-mcp" || candidate(server, c) {
				out = append(out, entry{name: id, config: c, node: plugin, disabled: scalar(field(plugin, "disabled")) == "true"})
			}
		}
	}
	return out, nil
}

func (d *document) merge(launcher, id, allow string) error {
	entries, err := d.entries()
	if err != nil || len(entries) > 1 {
		return errInvalid
	}
	var config map[string]any
	if len(entries) == 1 {
		config = entries[0].config
	} else {
		config = map[string]any{}
	}
	config["command"] = launcher
	if len(entries) == 0 {
		config["args"] = []string{"--client-id", strings.ReplaceAll(id, ":", "-"), "--allow", allow}
	}
	if config["enabled"] == false {
		config["enabled"] = true
	}
	if d.format == "yaml" {
		config["transport"] = "stdio"
		if len(entries) == 1 {
			var value yaml.Node
			if err = value.Encode(config); err != nil {
				return err
			}
			replaceYAML(entries[0].node, "config", &value)
			if n := field(entries[0].node, "disabled"); n != nil {
				n.Value = "false"
				n.Tag = "!!bool"
			}
			return nil
		}
		config["serverName"] = "pax"
		var patch yaml.Node
		if err = patch.Encode(map[string]any{"insert": []any{map[string]any{"id": "pax-mcp", "name": "@deepseek-ai/dsh-mcp-client", "config": config}}}); err != nil {
			return err
		}
		if len(d.yaml.Content) == 0 {
			d.yaml = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.SequenceNode, Tag: "!!seq"}}}
		}
		d.yaml.Content[0].Content = append(d.yaml.Content[0].Content, &patch)
		return nil
	}
	key := "mcp_servers"
	if d.format == "json" {
		key = "mcpServers"
		config["type"] = "stdio"
	}
	m, _ := d.object[key].(map[string]any)
	if m == nil {
		m = map[string]any{}
		d.object[key] = m
	}
	name := "pax"
	if len(entries) == 1 {
		name = entries[0].name
	}
	m[name] = config
	return nil
}
func replaceYAML(n *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			n.Content[i+1] = value
			return
		}
	}
}
func (d *document) encode() ([]byte, error) {
	switch d.format {
	case "toml":
		return toml.Marshal(d.object)
	case "json":
		b, e := json.MarshalIndent(d.object, "", "  ")
		return append(b, '\n'), e
	case "yaml":
		return yaml.Marshal(&d.yaml)
	}
	return nil, errInvalid
}
