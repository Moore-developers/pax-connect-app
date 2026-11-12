package aiprofile

import (
	"bytes"
	"encoding/json"
	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
	"io"
	"strings"
)

type nativeFile struct {
	Identity string `json:"-"`
	Name     string `json:"name"`
	Exists   bool   `json:"exists"`
	Data     []byte `json:"data"`
}
type template struct {
	Schema   int          `json:"schema"`
	Revision uint64       `json:"revision"`
	Files    []nativeFile `json:"files"`
}
type nativeValue struct{ url, key string }

const keySlot = "__PAX_API_KEY__"
const urlSlot = "__PAX_BASE_URL__"

func jsonObject(b []byte) (map[string]any, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var walk func() error
	walk = func() error {
		tok, err := d.Token()
		if err != nil {
			return Invalid
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		keys := map[string]bool{}
		for d.More() {
			if delim == '{' {
				k, e := d.Token()
				if e != nil {
					return Invalid
				}
				name, ok := k.(string)
				if !ok || keys[name] {
					return Invalid
				}
				keys[name] = true
			}
			if e := walk(); e != nil {
				return e
			}
		}
		_, err = d.Token()
		return err
	}
	if err := walk(); err != nil {
		return nil, Invalid
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, Invalid
	}
	var out map[string]any
	d = json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if d.Decode(&out) != nil || out == nil {
		return nil, Invalid
	}
	return out, nil
}
func yamlObject(b []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return map[string]any{}, nil
	}
	root, err := yamlDocument(b)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if root.Decode(&out) != nil || out == nil {
		return nil, Invalid
	}
	return out, nil
}
func object(v any) map[string]any { m, _ := v.(map[string]any); return m }
func text(v any) string           { s, _ := v.(string); return s }
func fileData(files []nativeFile, name string) []byte {
	for _, f := range files {
		if f.Name == name && f.Exists {
			return f.Data
		}
	}
	return nil
}
func replaceData(files []nativeFile, name string, b []byte) []nativeFile {
	out := append([]nativeFile(nil), files...)
	for i := range out {
		if out[i].Name == name {
			out[i].Exists = true
			out[i].Data = b
			return out
		}
	}
	return append(out, nativeFile{Name: name, Exists: true, Data: b})
}
func transform(cli CLI, files []nativeFile, base, key string) ([]nativeFile, nativeValue, error) {
	switch cli {
	case Codex:
		return codexConfig(files, base, key)
	case Claude:
		return claudeConfig(files, base, key)
	case DSH:
		return dshConfig(files, base, key)
	}
	return nil, nativeValue{}, Unsupported
}
func codexConfig(files []nativeFile, base, key string) ([]nativeFile, nativeValue, error) {
	var cfg map[string]any
	if toml.Unmarshal(fileData(files, "config.toml"), &cfg) != nil {
		return nil, nativeValue{}, Unsupported
	}
	providerID := text(cfg["model_provider"])
	provider := object(object(cfg["model_providers"])[providerID])
	auth, err := jsonObject(fileData(files, "auth.json"))
	if err != nil {
		return nil, nativeValue{}, Unsupported
	}
	v := nativeValue{url: text(provider["base_url"]), key: text(auth["OPENAI_API_KEY"])}
	if _, exists := auth["tokens"]; exists {
		return nil, v, Unsupported
	}
	if mode, exists := auth["auth_mode"]; exists && mode != "apikey" {
		return nil, v, Unsupported
	}
	if mode := text(cfg["forced_login_method"]); mode != "" && mode != "api" {
		return nil, v, Unsupported
	}
	if store := text(cfg["cli_auth_credentials_store"]); store != "" && store != "auto" && store != "file" {
		return nil, v, Unsupported
	}
	if cfg["profile"] != nil {
		return nil, v, Unsupported
	}
	if providerID == "" || providerID == "openai" {
		if provider == nil {
			provider = object(object(cfg["model_providers"])["openai"])
		}
		v.url = text(provider["base_url"])
		if v.url == "" {
			v.url = "https://api.openai.com/v1"
		}
	} else if provider == nil {
		return nil, v, Unsupported
	}
	if v.url == "" || v.key == "" || provider["auth"] != nil || provider["gateway_oauth"] != nil || text(provider["env_key"]) != "" || text(provider["experimental_bearer_token"]) != "" {
		return nil, v, Unsupported
	}
	for _, field := range []string{"http_headers", "env_http_headers"} {
		for name := range object(provider[field]) {
			if strings.EqualFold(name, "authorization") {
				return nil, v, Unsupported
			}
		}
	}
	if base == "" {
		return files, v, nil
	}
	if provider == nil {
		return nil, v, Unsupported
	}
	provider["base_url"] = base
	auth["OPENAI_API_KEY"] = key
	b, e := toml.Marshal(cfg)
	if e != nil {
		return nil, v, Invalid
	}
	a, e := json.MarshalIndent(auth, "", "  ")
	if e != nil {
		return nil, v, Invalid
	}
	return replaceData(replaceData(files, "config.toml", b), "auth.json", a), v, nil
}
func claudeConfig(files []nativeFile, base, key string) ([]nativeFile, nativeValue, error) {
	cfg, err := jsonObject(fileData(files, "settings.json"))
	if err != nil {
		return nil, nativeValue{}, Unsupported
	}
	env := object(cfg["env"])
	if cfg["apiKeyHelper"] != nil {
		return nil, nativeValue{}, Unsupported
	}
	for _, name := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY"} {
		if env[name] != nil {
			return nil, nativeValue{}, Unsupported
		}
	}
	field := "ANTHROPIC_AUTH_TOKEN"
	token, api := text(env[field]), text(env["ANTHROPIC_API_KEY"])
	if _, exists := env[field]; exists && env["ANTHROPIC_API_KEY"] != nil {
		return nil, nativeValue{}, Unsupported
	}
	if token == "" {
		field = "ANTHROPIC_API_KEY"
	}
	v := nativeValue{url: text(env["ANTHROPIC_BASE_URL"]), key: text(env[field])}
	if v.url == "" && api != "" {
		v.url = "https://api.anthropic.com"
	}
	if v.url == "" || v.key == "" {
		return nil, v, Unsupported
	}
	if base == "" {
		return files, v, nil
	}
	env["ANTHROPIC_BASE_URL"] = base
	env[field] = key
	b, e := json.MarshalIndent(cfg, "", "  ")
	if e != nil {
		return nil, v, Invalid
	}
	return replaceData(files, "settings.json", b), v, nil
}
func dshConfig(files []nativeFile, base, key string) ([]nativeFile, nativeValue, error) {
	if len(fileData(files, "profiles/desktop/cordis.patch.yml")) > 0 {
		return desktopDSHConfig(files, base, key)
	}
	settings, err := yamlObject(fileData(files, "settings.yaml"))
	if err != nil {
		return nil, nativeValue{}, Unsupported
	}
	creds, err := yamlObject(fileData(files, ".credentials.yaml"))
	if err != nil {
		return nil, nativeValue{}, Unsupported
	}
	adapter := object(settings["llm-deepseek"])
	ref := text(adapter["apiKeyEnv"])
	if ref == "" {
		ref = "DEEPSEEK_API_KEY"
	}
	refs := object(creds["refs"])
	v := nativeValue{url: text(adapter["baseURL"]), key: text(refs[ref])}
	if text(object(settings["agent-default-model"])["provider"]) != "deepseek-official" || v.url == "" || v.key == "" {
		return nil, v, Unsupported
	}
	if base == "" {
		return files, v, nil
	}
	adapter["baseURL"] = base
	refs[ref] = key
	b, e := yaml.Marshal(settings)
	if e != nil {
		return nil, v, Invalid
	}
	a, e := yaml.Marshal(creds)
	if e != nil {
		return nil, v, Invalid
	}
	return replaceData(replaceData(files, "settings.yaml", b), ".credentials.yaml", a), v, nil
}

func yamlDocument(b []byte) (yaml.Node, error) {
	var root yaml.Node
	if yaml.Unmarshal(b, &root) != nil {
		return root, Invalid
	}
	var valid func(*yaml.Node) bool
	valid = func(n *yaml.Node) bool {
		if n.Kind == yaml.AliasNode || n.Tag != "" && n.Tag != "!!map" && n.Tag != "!!seq" && n.Tag != "!!str" && n.Tag != "!!int" && n.Tag != "!!float" && n.Tag != "!!bool" && n.Tag != "!!null" {
			return false
		}
		seen := map[string]bool{}
		for i, c := range n.Content {
			if n.Kind == yaml.MappingNode && i%2 == 0 {
				if c.Kind != yaml.ScalarNode || seen[c.Value] {
					return false
				}
				seen[c.Value] = true
			}
			if !valid(c) {
				return false
			}
		}
		return true
	}

	if !valid(&root) {
		return root, Invalid
	}
	return root, nil
}
