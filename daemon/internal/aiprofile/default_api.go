package aiprofile

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// Built-in official APIs need no explicit base URL. When moving to a relay, add
// only a dedicated provider route; never reserialize user tables or auth documents.
func patchCodexBase(raw []byte, cfg map[string]any, old, next string) ([]byte, error) {
	provider := text(cfg["model_provider"])
	route := object(object(cfg["model_providers"])[provider])
	if text(route["base_url"]) != "" {
		return patchTOML(raw, []string{"model_providers", provider, "base_url"}, old, next)
	}
	if old == next {
		return raw, nil
	}
	if provider != "" && provider != "openai" || object(cfg["model_providers"])["pax"] != nil {
		return nil, Unsupported
	}
	var err error
	if _, exists := cfg["model_provider"]; exists {
		raw, err = patchTOML(raw, []string{"model_provider"}, provider, "pax")
		if err != nil {
			return nil, err
		}
	} else {
		raw = append([]byte("model_provider = \"pax\"\n"), raw...)
	}
	return append(raw, []byte("\n[model_providers.pax]\nname = \"Pax API\"\nbase_url = "+strconv.Quote(next)+"\nwire_api = \"responses\"\nrequires_openai_auth = true\n")...), nil
}

func patchClaudeBase(raw []byte, env map[string]any, old, next string) ([]byte, error) {
	if _, exists := env["ANTHROPIC_BASE_URL"]; exists {
		return patchJSON(raw, []string{"env", "ANTHROPIC_BASE_URL"}, old, next)
	}
	if old == next {
		return raw, nil
	}
	// Keep the entire surrounding settings document byte-for-byte.
	d := json.NewDecoder(bytes.NewReader(raw))
	if _, err := d.Token(); err != nil {
		return nil, Invalid
	}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return nil, Invalid
		}
		start := int(d.InputOffset())
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, Invalid
		}
		if key != "env" {
			continue
		}
		for start < len(raw) && bytes.ContainsRune([]byte(" \t\r\n:"), rune(raw[start])) {
			start++
		}
		encoded, _ := json.Marshal(next)
		nested, err := insertJSONProperty(raw[start:d.InputOffset()], "ANTHROPIC_BASE_URL", encoded)
		if err != nil {
			return nil, err
		}
		return spliceEdits(raw, []textEdit{{start, int(d.InputOffset()), string(nested)}})
	}
	return nil, Unsupported
}
