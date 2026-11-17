package config

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

// DocumentCredential describes presence only, never a value.
type DocumentCredential struct {
	Path    string
	Present bool
}

// DocumentSecretUpdate is a write-only change at an allowlisted JSON pointer.
type DocumentSecretUpdate struct {
	Path  string
	Value *string
	Clear bool
}

func secretPath(path string) bool {
	switch path {
	case "/username", "/password", "/uuid", "/obfs/password", "/transport/path", "/tls/reality/short_id", "/path":
		return true
	}
	for _, prefix := range []string{"/transport/headers/", "/headers/"} {
		if strings.HasPrefix(path, prefix) {
			part := strings.TrimPrefix(path, prefix)
			decoded := strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
			return part != "" && !strings.Contains(part, "/") && pointerPart(decoded) == part && !strings.EqualFold(decoded, "Host") && decoded != "__proto__" && decoded != "constructor" && decoded != "prototype"
		}
	}
	return false
}

// documentSecrets fails closed for credential-like fields outside the known policy.
func documentSecrets(object map[string]any) (map[string]any, error) {
	found := map[string]any{}
	var walk func(map[string]any, string) error
	walk = func(m map[string]any, parent string) error {
		for key, value := range m {
			path := parent + "/" + pointerPart(key)
			if secretPath(path) {
				found[path] = value
				continue
			}
			lower := strings.ToLower(key)
			if key == "__proto__" || key == "constructor" || key == "prototype" || key == "realm" || key == "client_metadata" {
				return errors.New("redaction_unsupported")
			}
			for _, suspect := range []string{"password", "username", "uuid", "token", "secret", "authorization", "cookie", "private", "client_key"} {
				if strings.Contains(lower, suspect) {
					return errors.New("redaction_unsupported")
				}
			}
			if child, ok := value.(map[string]any); ok {
				if err := walk(child, path); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return found, walk(object, "")
}
func secretParent(object map[string]any, path string, createHeaders bool) (map[string]any, string) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	current := object
	for _, escaped := range parts[:len(parts)-1] {
		key := strings.ReplaceAll(strings.ReplaceAll(escaped, "~1", "/"), "~0", "~")
		child, ok := current[key].(map[string]any)
		if !ok && key == "headers" && createHeaders && current[key] == nil {
			child = map[string]any{}
			current[key] = child
			ok = true
		}
		if !ok {
			return nil, ""
		}
		current = child
	}
	return current, strings.ReplaceAll(strings.ReplaceAll(parts[len(parts)-1], "~1", "/"), "~0", "~")
}

// RedactRouteDocument removes credentials at the daemon's private model boundary.
func RedactRouteDocument(raw []byte) ([]byte, []DocumentCredential, error) {
	object, err := ParseDocument(raw)
	if err != nil {
		return nil, nil, err
	}
	found, err := documentSecrets(object)
	if err != nil {
		return nil, nil, err
	}
	paths := map[string]bool{}
	for _, path := range []string{"/username", "/password", "/uuid", "/obfs/password", "/transport/path", "/tls/reality/short_id", "/path"} {
		paths[path] = false
	}
	for path, value := range found {
		paths[path] = value != nil && value != ""
		parent, key := secretParent(object, path, false)
		delete(parent, key)
	}
	keys := make([]string, 0, len(paths))
	for path := range paths {
		keys = append(keys, path)
	}
	sort.Strings(keys)
	credentials := make([]DocumentCredential, 0, len(keys))
	for _, path := range keys {
		credentials = append(credentials, DocumentCredential{path, paths[path]})
	}
	encoded, err := json.Marshal(object)
	return encoded, credentials, err
}

// MergeRouteSecrets applies explicit changes after the owner has checked the same version.
// Missing non-secret fields remain deleted; removed protocol containers are not resurrected.
func MergeRouteSecrets(raw, previous []byte, updates []DocumentSecretUpdate) ([]byte, error) {
	object, err := ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	supplied, err := documentSecrets(object)
	if err != nil {
		return nil, err
	}
	if len(supplied) > 0 {
		return nil, errors.New("secret_field_requires_update")
	}
	if len(previous) > 0 {
		old, e := ParseDocument(previous)
		if e != nil {
			return nil, e
		}
		found, e := documentSecrets(old)
		if e != nil {
			return nil, e
		}
		if stringField(old, "type") == stringField(object, "type") {
			for path, value := range found {
				if strings.HasPrefix(path, "/transport/") && stringField(objectField(old, "transport"), "type") != stringField(objectField(object, "transport"), "type") {
					continue
				}
				parent, key := secretParent(object, path, true)
				if parent != nil {
					parent[key] = value
				}
			}
		}
	}
	if len(updates) > 32 {
		return nil, errors.New("secret_update_invalid")
	}
	seen := map[string]bool{}
	total := 0
	for _, update := range updates {
		total += len(update.Path)
		if !secretPath(update.Path) || seen[update.Path] || (update.Value == nil) == !update.Clear {
			return nil, errors.New("secret_update_invalid")
		}
		seen[update.Path] = true
		if update.Value != nil {
			total += len(*update.Value)
			if len(*update.Value) > 16<<10 || *update.Value == "***" || *update.Value == "[redacted]" || strings.HasPrefix(*update.Value, "[redacted_") {
				return nil, errors.New("secret_update_invalid")
			}
		}
		if total > MaxDocumentBytes {
			return nil, errors.New("secret_update_invalid")
		}
		parent, key := secretParent(object, update.Path, true)
		if parent == nil {
			return nil, errors.New("secret_update_invalid")
		}
		if update.Clear {
			delete(parent, key)
		} else {
			parent[key] = *update.Value
		}
	}
	if requiredSecretsMissing(object) {
		return nil, errors.New("credentials_required")
	}
	encoded, err := json.Marshal(object)
	if len(encoded) > MaxDocumentBytes {
		return nil, ErrRouteDocument
	}
	return encoded, err
}
func requiredSecretsMissing(m map[string]any) bool {
	switch stringField(m, "type") {
	case "vmess", "vless":
		return stringField(m, "uuid") == ""
	case "hysteria2", "trojan", "anytls", "shadowsocks":
		return stringField(m, "password") == ""
	case "socks", "http":
		return (stringField(m, "username") == "") != (stringField(m, "password") == "")
	}
	return false
}
