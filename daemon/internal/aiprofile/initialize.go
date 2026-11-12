package aiprofile

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unicode"

	"github.com/pelletier/go-toml/v2"
)

// initializationState distinguishes absent API settings from existing account
// authentication. It never returns credentials or treats unreadable files as empty.
func (m *Manager) initializationState(cli CLI, files []nativeFile) (string, error) {
	switch cli {
	case Claude:
		account, err := m.claudeAccount()
		if err != nil {
			return "invalid", err
		}
		if account {
			return "account", Unsupported
		}
		// Claude's account credentials live alongside settings; they are never a write target.
		if _, err := os.Lstat(filepath.Join(m.targetDir(cli), ".credentials.json")); err == nil {
			return "account", Unsupported
		} else if !errors.Is(err, os.ErrNotExist) {
			return "invalid", Unsafe
		}
		cfg := map[string]any{}
		if raw := fileData(files, "settings.json"); len(bytes.TrimSpace(raw)) > 0 {
			var err error
			cfg, err = jsonObject(raw)
			if err != nil {
				return "invalid", Invalid
			}
		} else {
			for _, f := range files {
				if f.Exists {
					return "invalid", Invalid
				}
			}
		}
		if v, ok := cfg["env"]; ok {
			env, good := v.(map[string]any)
			if !good {
				return "invalid", Unsupported
			}
			for _, k := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY"} {
				if _, exists := env[k]; exists {
					return "invalid", Unsupported
				}
			}
		}
		if _, ok := cfg["apiKeyHelper"]; ok {
			return "account", Unsupported
		}
		return "missing", nil
	case Codex:
		cfg := map[string]any{}
		if raw := fileData(files, "config.toml"); len(raw) > 0 {
			if toml.Unmarshal(raw, &cfg) != nil {
				return "invalid", Invalid
			}
		}
		if raw := fileData(files, "auth.json"); len(raw) > 0 {
			auth, err := jsonObject(raw)
			if err != nil {
				return "invalid", Invalid
			}
			if _, ok := auth["tokens"]; ok {
				return "account", Unsupported
			}
			if mode, ok := auth["auth_mode"]; ok && mode != "apikey" {
				return "account", Unsupported
			}
			if len(auth) > 0 {
				return "invalid", Unsupported
			}
		} else {
			for _, f := range files {
				if f.Name == "auth.json" && f.Exists {
					return "invalid", Invalid
				}
			}
		}
		for _, k := range []string{"model_provider", "model_providers", "forced_login_method", "cli_auth_credentials_store"} {
			if _, ok := cfg[k]; ok {
				return "invalid", Unsupported
			}
		}
		return "missing", nil
	default:
		return "invalid", Unsupported
	}
}

func (m *Manager) initializeAPI(cli CLI, files []nativeFile, base, key, model string) ([]nativeFile, error) {
	state, err := m.initializationState(cli, files)
	if err != nil || state != "missing" {
		return nil, Unsupported
	}
	if len(model) > 256 || strings.IndexFunc(model, unicode.IsControl) >= 0 {
		return nil, Invalid
	}
	switch cli {
	case Claude:
		raw := fileData(files, "settings.json")
		if len(raw) == 0 {
			raw = []byte("{}\n")
		}
		cfg, err := jsonObject(raw)
		if err != nil {
			return nil, err
		}
		env := object(cfg["env"])
		if env == nil {
			env = map[string]any{}
		}
		env["ANTHROPIC_BASE_URL"] = base
		env["ANTHROPIC_AUTH_TOKEN"] = key
		// Change only the env value, or insert it; all unrelated settings keep their bytes.
		value, err := json.Marshal(env)
		if err != nil {
			return nil, Invalid
		}
		raw, err = insertJSONProperty(raw, "env", value)
		if err != nil {
			return nil, err
		}
		return replaceData(files, "settings.json", raw), nil
	case Codex:
		raw := fileData(files, "config.toml")
		cfg := map[string]any{}
		if toml.Unmarshal(raw, &cfg) != nil {
			return nil, Invalid
		}
		if existing, ok := cfg["model"]; ok {
			if text(existing) == "" {
				return nil, Unsupported
			}
		} else {
			if strings.TrimSpace(model) == "" {
				return nil, Invalid
			}
			raw = append([]byte("model = "+strconv.Quote(model)+"\n"), raw...)
		}
		// Root scalars must precede any user tables; the provider table is appended.
		raw = append([]byte("model_provider = \"pax\"\n"), raw...)
		raw = append(raw, []byte("\n[model_providers.pax]\nname = \"Pax API\"\nbase_url = "+strconv.Quote(base)+"\nwire_api = \"responses\"\nrequires_openai_auth = true\n")...)
		auth, err := json.Marshal(map[string]string{"auth_mode": "apikey", "OPENAI_API_KEY": key})
		if err != nil {
			return nil, Invalid
		}
		return replaceData(replaceData(files, "config.toml", raw), "auth.json", auth), nil
	}
	return nil, Unsupported
}

// insertJSONProperty preserves all bytes outside a single top-level value.
func insertJSONProperty(raw []byte, key string, value []byte) ([]byte, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := d.Token(); err != nil || tok != json.Delim('{') {
		return nil, Invalid
	}
	hasFields := false
	for d.More() {
		hasFields = true
		tok, err := d.Token()
		if err != nil {
			return nil, Invalid
		}
		start := int(d.InputOffset())
		var ignored json.RawMessage
		if d.Decode(&ignored) != nil {
			return nil, Invalid
		}
		if tok == key {
			for start < len(raw) && bytes.ContainsRune([]byte(" \t\r\n:"), rune(raw[start])) {
				start++
			}
			return append(append(append([]byte(nil), raw[:start]...), value...), raw[d.InputOffset():]...), nil
		}
	}
	at := int(d.InputOffset())
	for at < len(raw) && raw[at] != '}' {
		at++
	}
	if at == len(raw) {
		return nil, Invalid
	}
	name, _ := json.Marshal(key)
	property := append(append(name, ':'), value...)
	if hasFields {
		property = append([]byte(","), property...)
	}
	return append(append(append([]byte(nil), raw[:at]...), property...), raw[at:]...), nil
}

func (m *Manager) prepareTargetDirectory(cli CLI) error {
	dir := m.targetDir(cli)
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		if err = m.safeParent(filepath.Dir(dir)); err != nil {
			return err
		}
		if err = os.Mkdir(dir, 0700); err != nil {
			return Changed
		}
		if err = syncDirectory(filepath.Dir(dir)); err != nil {
			return err
		}
	}
	return m.safeParent(dir)
}

// Claude on macOS may keep tokens in Keychain and only the account identity in
// .claude.json. Read this marker without reading or exposing any Keychain secret.
func (m *Manager) claudeAccount() (bool, error) {
	path := filepath.Join(m.home, ".claude.json")
	if m.directories[Claude] != "" {
		path = filepath.Join(m.targetDir(Claude), ".claude.json")
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, Unsafe
	}
	file := os.NewFile(uintptr(fd), "cli-account-status")
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
		return false, Unsafe
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Uid != m.owner {
		return false, Unsafe
	}
	raw, err := io.ReadAll(io.LimitReader(file, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		return false, Unsafe
	}
	cfg, err := jsonObject(raw)
	if err != nil {
		return false, Invalid
	}
	account, exists := cfg["oauthAccount"]
	return exists && account != nil, nil
}

func codexNeedsModel(files []nativeFile) bool {
	var cfg map[string]any
	if toml.Unmarshal(fileData(files, "config.toml"), &cfg) != nil {
		return true
	}
	return text(cfg["model"]) == ""
}
