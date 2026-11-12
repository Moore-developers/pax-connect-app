package aiprofile

import (
	"encoding/binary"
	"encoding/json"
	"gopkg.in/yaml.v3"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

type asarEntry struct {
	Files  map[string]asarEntry `json:"files"`
	Offset string               `json:"offset"`
	Size   int64                `json:"size"`
}

// desktopDSHVersion reads the installed app archive index without executing its JavaScript.
func desktopDSHVersion(home string) string {
	found := false
	for _, app := range []string{"/Applications/DeepSeek Harness.app", filepath.Join(home, "Applications", "DeepSeek Harness.app")} {
		if _, e := os.Stat(app); e == nil {
			found = true
		}
		f, e := os.Open(filepath.Join(app, "Contents", "Resources", "app.asar"))
		if e != nil {
			continue
		}
		h := make([]byte, 16)
		if _, e = io.ReadFull(f, h); e != nil {
			_ = f.Close()
			continue
		}
		length := binary.LittleEndian.Uint32(h[12:])
		if length > 8<<20 {
			_ = f.Close()
			continue
		}
		b := make([]byte, length)
		if _, e = io.ReadFull(f, b); e != nil {
			_ = f.Close()
			continue
		}
		var root asarEntry
		if json.Unmarshal(b, &root) != nil {
			_ = f.Close()
			continue
		}
		entry := root.Files["dsh"].Files["package.json"]
		offset, offsetError := strconv.ParseInt(entry.Offset, 10, 64)
		if offsetError != nil || offset < 0 || offset > 1<<40 || entry.Size <= 0 || entry.Size > 128<<10 {
			_ = f.Close()
			continue
		}
		b = make([]byte, entry.Size)
		_, e = f.ReadAt(b, int64(8+binary.LittleEndian.Uint32(h[4:]))+offset)
		_ = f.Close()
		if e != nil {
			continue
		}
		var pkg struct{ Version string }
		if json.Unmarshal(b, &pkg) != nil {
			continue
		}
		modules := root.Files["dsh"].Files["node_modules"].Files["@deepseek-ai"].Files
		if pkg.Version == "0.2.1-rc.3" && modules["dsh-settings"].Files != nil && modules["dsh-llm-deepseek-api-key"].Files != nil && modules["dsh-credentials-local"].Files != nil {
			return pkg.Version
		}
	}
	if found {
		return "unsupported"
	}
	return ""
}
func patchRows(files []nativeFile) ([]map[string]any, error) {
	b := fileData(files, "profiles/desktop/cordis.patch.yml")
	var rows []map[string]any
	if len(b) == 0 {
		return nil, Unsupported
	}
	doc, err := yamlDocument(b)
	if err != nil || doc.Decode(&rows) != nil {
		return nil, Unsupported
	}
	return rows, nil
}
func patchConfig(rows []map[string]any, name string) map[string]any {
	for _, r := range rows {
		if text(r["name"]) == name {
			return object(r["config"])
		}
	}
	return nil
}
func desktopDSHConfig(files []nativeFile, base, key string) ([]nativeFile, nativeValue, error) {
	rows, e := patchRows(files)
	if e != nil {
		return nil, nativeValue{}, e
	}
	selected := patchConfig(rows, "@deepseek-ai/dsh-agent-default-model")
	adapter := patchConfig(rows, "@deepseek-ai/dsh-llm-deepseek-api-key")
	creds, e := yamlObject(fileData(files, ".credentials.yaml"))
	if e != nil {
		return nil, nativeValue{}, e
	}
	refs := object(creds["refs"])
	ref := text(adapter["apiKeyEnv"])
	v := nativeValue{url: text(adapter["baseURL"]), key: text(refs[ref])}
	if text(selected["provider"]) != "deepseek-official" || v.url == "" || v.key == "" {
		return nil, v, Unsupported
	}
	if base == "" {
		return files, v, nil
	}
	adapter["baseURL"] = base
	refs[ref] = key
	b, e := yaml.Marshal(rows)
	if e != nil {
		return nil, v, Invalid
	}
	a, e := yaml.Marshal(creds)
	if e != nil {
		return nil, v, Invalid
	}
	return replaceData(replaceData(files, "profiles/desktop/cordis.patch.yml", b), ".credentials.yaml", a), v, nil
}
