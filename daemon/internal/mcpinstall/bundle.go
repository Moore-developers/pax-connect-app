package mcpinstall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Bundle is a verified, complete App build. Paths are canonical absolute paths.
type Bundle struct {
	Path        string `json:"bundle_path"`
	CodeHash    string `json:"code_hash"`
	Fingerprint string `json:"daemon_fingerprint"`
	Build       string `json:"build"`
}

// Verify checks the sealed bundle and hashes of the signed paired executables.
// Ad-hoc development signatures are supported without inventing a TeamID.
func Verify(ctx context.Context, path string) (Bundle, error) {
	if !filepath.IsAbs(path) {
		return Bundle{}, errors.New("app_unavailable")
	}
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return Bundle{}, errors.New("app_unavailable")
	}
	if err = exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", path).Run(); err != nil {
		return Bundle{}, errors.New("app_unavailable")
	}
	contents := filepath.Join(path, "Contents")
	plistValue := func(key string) string {
		value, e := exec.CommandContext(ctx, "/usr/libexec/PlistBuddy", "-c", "Print :"+key, filepath.Join(contents, "Info.plist")).Output()
		if e != nil {
			return ""
		}
		return strings.TrimSpace(string(value))
	}
	if plistValue("CFBundleIdentifier") != "dev.moore.PaxConnect" {
		return Bundle{}, errors.New("app_mismatch")
	}
	details, err := exec.CommandContext(ctx, "/usr/bin/codesign", "-d", "--verbose=4", path).CombinedOutput()
	if err != nil {
		return Bundle{}, errors.New("app_unavailable")
	}
	codeHash := ""
	for _, line := range strings.Split(string(details), "\n") {
		if strings.HasPrefix(line, "CDHash=") {
			codeHash = strings.TrimPrefix(line, "CDHash=")
		}
	}
	if codeHash == "" {
		return Bundle{}, errors.New("app_mismatch")
	}
	daemon, err := os.ReadFile(filepath.Join(contents, "MacOS/pax-connectd"))
	if err != nil {
		return Bundle{}, errors.New("app_unavailable")
	}
	plist, err := os.ReadFile(filepath.Join(contents, "Library/LaunchDaemons/dev.moore.pax-connect.daemon.plist"))
	if err != nil {
		return Bundle{}, errors.New("app_unavailable")
	}
	hash := sha256.New()
	_, _ = hash.Write(daemon)
	_, _ = hash.Write(plist)
	fingerprint := hex.EncodeToString(hash.Sum(nil))
	expected, err := os.ReadFile(filepath.Join(contents, "Resources/daemon-fingerprint.txt"))
	if err != nil || strings.TrimSpace(string(expected)) != fingerprint {
		return Bundle{}, errors.New("app_mismatch")
	}
	manifest, err := os.ReadFile(filepath.Join(contents, "Resources/mcp-fingerprints.json"))
	var hashes map[string]string
	if err != nil || json.Unmarshal(manifest, &hashes) != nil {
		return Bundle{}, errors.New("app_mismatch")
	}
	for _, name := range []string{"pax-mcp", "pax-mcp-launcher"} {
		actual, e := fileHash(filepath.Join(contents, "MacOS", name))
		if e != nil || hashes[name] != actual {
			return Bundle{}, errors.New("app_mismatch")
		}
	}
	return Bundle{Path: path, CodeHash: codeHash, Fingerprint: fingerprint, Build: plistValue("CFBundleVersion")}, nil
}

func fileHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

// Socket resolves the path through the already verified bundled daemon.
func (b Bundle) Socket(ctx context.Context) (string, error) {
	data, err := exec.CommandContext(ctx, filepath.Join(b.Path, "Contents/MacOS/pax-connectd"), "--print-user-socket").Output()
	path := strings.TrimSpace(string(data))
	if err != nil || !filepath.IsAbs(path) || len(path) >= 104 {
		return "", errors.New("app_unavailable")
	}
	return path, nil
}
