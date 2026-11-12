package aiprofile

import (
	"bytes"
	"encoding/json"
	"gopkg.in/yaml.v3"
	"strings"
)

func yamlField(n *yaml.Node, key string) *yaml.Node {
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
func yamlRow(root *yaml.Node, name string) *yaml.Node {
	if root.Kind != yaml.SequenceNode {
		return nil
	}
	var found *yaml.Node
	for _, row := range root.Content {
		n := yamlField(row, "name")
		if n != nil && n.Value == name {
			if found != nil {
				return nil
			}
			found = yamlField(row, "config")
		}
	}
	return found
}
func patchDSH(files []nativeFile, old nativeValue, base, key string) ([]nativeFile, error) {
	settingsName := "settings.yaml"
	desktop := len(fileData(files, "profiles/desktop/cordis.patch.yml")) > 0
	if desktop {
		settingsName = "profiles/desktop/cordis.patch.yml"
	}
	settings, e := yamlDocument(fileData(files, settingsName))
	if e != nil || len(settings.Content) != 1 {
		return nil, Invalid
	}
	creds, e := yamlDocument(fileData(files, ".credentials.yaml"))
	if e != nil || len(creds.Content) != 1 {
		return nil, Invalid
	}
	version := yamlField(creds.Content[0], "version")
	if version == nil || version.Kind != yaml.ScalarNode || version.Tag != "!!int" || version.Value != "1" {
		return nil, Unsupported
	}
	adapter := yamlField(settings.Content[0], "llm-deepseek")
	if desktop {
		adapter = yamlRow(settings.Content[0], "@deepseek-ai/dsh-llm-deepseek-api-key")
	}
	ref := yamlField(adapter, "apiKeyEnv")
	refName := "DEEPSEEK_API_KEY"
	if ref != nil {
		if ref.Kind != yaml.ScalarNode || ref.Tag != "!!str" {
			return nil, Unsupported
		}
		refName = ref.Value
	}
	references := 0
	var count func(*yaml.Node)
	count = func(n *yaml.Node) {
		if n.Kind == yaml.MappingNode {
			for i := 0; i < len(n.Content); i += 2 {
				if n.Content[i].Value == "apiKeyEnv" && n.Content[i+1].Value == refName {
					references++
				}
			}
		}
		for _, c := range n.Content {
			count(c)
		}
	}
	count(settings.Content[0])
	if ref == nil {
		references++
	}
	if references > 1 {
		return nil, Unsupported
	}
	address := yamlField(adapter, "baseURL")
	credential := yamlField(yamlField(creds.Content[0], "refs"), refName)
	b, e := patchYAMLScalar(fileData(files, settingsName), address, old.url, base)
	if e != nil {
		return nil, e
	}
	a, e := patchYAMLScalar(fileData(files, ".credentials.yaml"), credential, old.key, key)
	if e != nil {
		return nil, e
	}
	out := replaceData(replaceData(files, settingsName, b), ".credentials.yaml", a)
	_, v, e := transform(DSH, out, "", "")
	if e != nil || v.url != base || v.key != key {
		return nil, Invalid
	}
	return out, nil
}

func patchYAMLScalar(raw []byte, n *yaml.Node, old, next string) ([]byte, error) {
	if n == nil || n.Kind != yaml.ScalarNode || n.Tag != "!!str" || n.Value != old || n.Anchor != "" || n.Style&(yaml.LiteralStyle|yaml.FoldedStyle|yaml.TaggedStyle) != 0 {
		return nil, Unsupported
	}
	lines := bytes.SplitAfter(raw, []byte("\n"))
	if n.Line < 1 || n.Line > len(lines) {
		return nil, Invalid
	}
	start := 0
	for _, line := range lines[:n.Line-1] {
		start += len(line)
	}
	runes := []rune(string(lines[n.Line-1]))
	if n.Column < 1 || n.Column > len(runes) {
		return nil, Invalid
	}
	start += len(string(runes[:n.Column-1]))
	end := start
	if raw[start] == 39 || raw[start] == 34 {
		quote := raw[start]
		end++
		for end < len(raw) && raw[end] != 10 && raw[end] != 13 {
			if quote == 34 && raw[end] == 92 {
				end += 2
				continue
			}
			if raw[end] == quote {
				if quote == 39 && end+1 < len(raw) && raw[end+1] == quote {
					end += 2
					continue
				}
				end++
				break
			}
			end++
		}
		if end > len(raw) || end <= start+1 || raw[end-1] != quote {
			return nil, Unsupported
		}
	} else {
		for end < len(raw) && !strings.ContainsRune("\r\n,]}", rune(raw[end])) {
			if raw[end] == '#' && end > start && (raw[end-1] == ' ' || raw[end-1] == 9) {
				break
			}
			end++
		}
		for end > start && (raw[end-1] == ' ' || raw[end-1] == 9) {
			end--
		}
	}
	if old == next {
		return append([]byte(nil), raw...), nil
	}
	encoded, _ := json.Marshal(next)
	out, e := spliceEdits(raw, []textEdit{{start, end, string(encoded)}})
	if e != nil {
		return nil, e
	}
	if _, e = yamlDocument(out); e != nil {
		return nil, Invalid
	}
	return out, nil
}
