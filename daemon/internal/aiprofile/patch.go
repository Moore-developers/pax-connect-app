package aiprofile

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

type textEdit struct {
	start, end int
	value      string
}

// patchCurrent only replaces existing, unambiguous API credential scalars.
// Every other byte, including comments, remains owned by the CLI user.
func patchCurrent(cli CLI, files []nativeFile, base, key string) ([]nativeFile, error) {
	for _, f := range files {
		if cli == Codex && f.Name == "config.toml" && (!f.Exists || len(bytes.TrimSpace(f.Data)) == 0) {
			continue
		}
		if !f.Exists || len(bytes.TrimSpace(f.Data)) == 0 {
			return nil, Unsupported
		}
	}
	_, old, e := transform(cli, files, "", "")
	if e != nil {
		return nil, e
	}
	out := append([]nativeFile(nil), files...)
	switch cli {
	case Codex:
		var cfg map[string]any
		if toml.Unmarshal(fileData(files, "config.toml"), &cfg) != nil {
			return nil, Invalid
		}
		provider := text(cfg["model_provider"])
		route := object(object(cfg["model_providers"])[provider])
		for _, field := range []string{"env_key", "experimental_bearer_token"} {
			if _, exists := route[field]; exists {
				return nil, Unsupported
			}
		}
		for _, field := range []string{"http_headers", "env_http_headers"} {
			for name := range object(route[field]) {
				if strings.EqualFold(name, "authorization") {
					return nil, Unsupported
				}
			}
		}
		auth, err := jsonObject(fileData(files, "auth.json"))
		if err != nil {
			return nil, Invalid
		}
		if mode, exists := auth["auth_mode"]; exists && mode != "apikey" {
			return nil, Unsupported
		}
		if _, exists := auth["tokens"]; exists {
			return nil, Unsupported
		}
		// Another native profile may explicitly reference the same provider.
		for _, entry := range object(cfg["profiles"]) {
			if value, exists := object(entry)["model_provider"]; exists && text(value) == provider {
				return nil, Unsupported
			}
		}
		b, err := patchCodexBase(fileData(files, "config.toml"), cfg, old.url, base)
		if err != nil {
			return nil, err
		}
		a, err := patchJSON(fileData(files, "auth.json"), []string{"OPENAI_API_KEY"}, old.key, key)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(fileData(files, "config.toml"), b) {
			out = replaceData(out, "config.toml", b)
		}
		out = replaceData(out, "auth.json", a)
	case Claude:
		cfg, err := jsonObject(fileData(files, "settings.json"))
		if err != nil {
			return nil, Invalid
		}
		env := object(cfg["env"])
		field := "ANTHROPIC_AUTH_TOKEN"
		if text(env[field]) == "" {
			field = "ANTHROPIC_API_KEY"
		}
		// Two configured authentication fields are not an unambiguous route.
		other := "ANTHROPIC_API_KEY"
		if field == other {
			other = "ANTHROPIC_AUTH_TOKEN"
		}
		if _, exists := env[other]; exists {
			return nil, Unsupported
		}
		b, err := patchClaudeBase(fileData(files, "settings.json"), env, old.url, base)
		if err != nil {
			return nil, err
		}
		b, err = patchJSON(b, []string{"env", field}, old.key, key)
		if err != nil {
			return nil, err
		}
		out = replaceData(out, "settings.json", b)
	case DSH:
		return patchDSH(files, old, base, key)
	default:
		return nil, Unsupported
	}
	_, applied, e := transform(cli, out, "", "")
	if e != nil || applied.url != base || applied.key != key {
		return nil, Invalid
	}
	return out, nil
}

func spliceEdits(raw []byte, edits []textEdit) ([]byte, error) {
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	out := append([]byte(nil), raw...)
	previous := len(raw)
	for _, edit := range edits {
		if edit.start < 0 || edit.end > previous || edit.start >= edit.end {
			return nil, Unsupported
		}
		out = append(append(append([]byte(nil), out[:edit.start]...), []byte(edit.value)...), out[edit.end:]...)
		previous = edit.start
	}
	return out, nil
}

func patchJSON(raw []byte, path []string, old, next string) ([]byte, error) {
	if _, e := jsonObject(raw); e != nil {
		return nil, Invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var edits []textEdit
	var walk func([]string) error
	walk = func(current []string) error {
		start := int(d.InputOffset())
		tok, e := d.Token()
		if e != nil {
			return Invalid
		}
		if reflect.DeepEqual(current, path) {
			value, ok := tok.(string)
			if !ok || value != old {
				return Unsupported
			}
			for start < len(raw) && bytes.ContainsRune([]byte(" :,\t\r\n"), rune(raw[start])) {
				start++
			}
			encoded, _ := json.Marshal(next)
			edits = append(edits, textEdit{start, int(d.InputOffset()), string(encoded)})
		}
		if delim, ok := tok.(json.Delim); ok {
			for d.More() {
				child := append([]string(nil), current...)
				if delim == '{' {
					k, err := d.Token()
					if err != nil {
						return Invalid
					}
					child = append(child, k.(string))
				} else {
					child = append(child, "[]")
				}
				if e = walk(child); e != nil {
					return e
				}
			}
			if _, e = d.Token(); e != nil {
				return Invalid
			}
		}
		return nil
	}
	if e := walk(nil); e != nil {
		return nil, e
	}
	if len(edits) != 1 {
		return nil, Unsupported
	}
	if old == next {
		return append([]byte(nil), raw...), nil
	}
	out, e := spliceEdits(raw, edits)
	if e != nil {
		return nil, e
	}
	if _, e = jsonObject(out); e != nil {
		return nil, Invalid
	}
	return out, nil
}

// The pinned TOML parser supplies source ranges; no map re-serialization occurs.
func patchTOML(raw []byte, path []string, old, next string) ([]byte, error) {
	var document map[string]any
	if toml.Unmarshal(raw, &document) != nil {
		return nil, Invalid
	}
	var parser unstable.Parser
	parser.Reset(raw)
	var context []string
	var edits []textEdit
	var visit func(*unstable.Node, []string) error
	visit = func(n *unstable.Node, prefix []string) error {
		keys := append([]string(nil), prefix...)
		it := n.Key()
		for it.Next() {
			keys = append(keys, string(it.Node().Data))
		}
		value := n.Child()
		if reflect.DeepEqual(keys, path) {
			if value.Kind != unstable.String || string(value.Data) != old {
				return Unsupported
			}
			encoded, _ := json.Marshal(next)
			edits = append(edits, textEdit{int(value.Raw.Offset), int(value.Raw.Offset + value.Raw.Length), string(encoded)})
		}
		if value.Kind == unstable.InlineTable {
			it := value.Children()
			for it.Next() {
				if e := visit(it.Node(), keys); e != nil {
					return e
				}
			}
		}
		return nil
	}
	for parser.NextExpression() {
		n := parser.Expression()
		switch n.Kind {
		case unstable.Table, unstable.ArrayTable:
			context = nil
			it := n.Key()
			for it.Next() {
				context = append(context, string(it.Node().Data))
			}
			if n.Kind == unstable.ArrayTable {
				context = append(context, "[]")
			}
		case unstable.KeyValue:
			if e := visit(n, context); e != nil {
				return nil, e
			}
		}
	}
	if parser.Error() != nil {
		return nil, Invalid
	}
	if len(edits) != 1 {
		return nil, Unsupported
	}
	if old == next {
		return append([]byte(nil), raw...), nil
	}
	out, e := spliceEdits(raw, edits)
	if e != nil {
		return nil, e
	}
	// Decode into a fresh map: reusing array-table slices can panic in go-toml.
	var validated map[string]any
	if toml.Unmarshal(out, &validated) != nil {
		return nil, Invalid
	}
	return out, nil
}
