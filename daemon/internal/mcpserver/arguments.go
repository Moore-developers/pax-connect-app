package mcpserver

import (
	"bytes"
	"encoding/json"
	"io"
)

// Check before map decoding so duplicate document keys cannot be normalized away.
func parseArguments(raw []byte) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	if len(raw) > 3<<20 {
		return nil, invalidArgument()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 36 {
			return invalidArgument()
		}
		token, err := decoder.Token()
		if err != nil {
			return invalidArgument()
		}
		if delim, ok := token.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for decoder.More() {
					key, err := decoder.Token()
					if err != nil {
						return invalidArgument()
					}
					name, ok := key.(string)
					if !ok || seen[name] {
						return invalidArgument()
					}
					seen[name] = true
					if err := walk(depth + 1); err != nil {
						return err
					}
				}
			case '[':
				for decoder.More() {
					if err := walk(depth + 1); err != nil {
						return err
					}
				}
			default:
				return invalidArgument()
			}
			if _, err := decoder.Token(); err != nil {
				return invalidArgument()
			}
		}
		return nil
	}
	if err := walk(0); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, invalidArgument()
	}
	var args map[string]any
	if json.Unmarshal(raw, &args) != nil || args == nil {
		return nil, invalidArgument()
	}
	return args, nil
}
