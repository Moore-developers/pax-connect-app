package core

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Same pinned hashes as rulesets/manifest.json and the approved bundle
// install. Do not trust a checksum file from the user-writable resources tree.
var bundledRules = map[string]string{
	"geosite-google.srs": "992d7ba91ce9c6e1c248d9ef0d58b2a17281415526b6a560dce6368e4f72e1f2",
	"geosite-cn.srs":     "2f4af0be08f2ff494a8fcdef992dc92a2267600ce26c9bba17421d1ce9a6715c",
	"geoip-cn.srs":       "ebee603fdf402314b44b9f653cdcf6d9cc9c41e84e2b3515e3123c5c920a93bc",
}

func copyRuleSets(o Options) error {
	dir := filepath.Join(o.State, "rulesets")
	if err := protectedDirectory(dir, 0700); err != nil {
		return err
	}
	return copyPinnedRules(o.Resources, dir, bundledRules)
}

func copyPinnedRules(resources, destination string, expected map[string]string) error {
	// Hold the directory fd so replacing resources after opening it cannot switch
	// later file opens. O_NONBLOCK avoids hanging on a malicious FIFO.
	fd, err := unix.Open(resources, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return errors.New("core_ruleset_source_unsafe")
	}
	defer func() { _ = unix.Close(fd) }()
	for name, digest := range expected {
		if err = copyPinnedRule(fd, name, destination, digest); err != nil {
			return err
		}
	}
	return nil
}
func copyPinnedRule(sourceDir int, name, destination, digest string) error {
	fd, err := unix.Openat(sourceDir, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return errors.New("core_ruleset_source_unsafe")
	}
	source := os.NewFile(uintptr(fd), name)
	defer func() { _ = source.Close() }()
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("core_ruleset_source_unsafe")
	}
	// Temporary file + rename never follows a destination symlink. The protected
	// directory has no user writers; sing-box only watches the root-owned copy.
	target, err := os.CreateTemp(destination, ".ruleset-")
	if err != nil {
		return errors.New("core_ruleset_copy_failed")
	}
	defer func() { _ = target.Close(); _ = os.Remove(target.Name()) }()
	h := sha256.New()
	// Bound a malicious/mutating source even before its fixed hash is checked.
	n, err := io.Copy(io.MultiWriter(target, h), io.LimitReader(source, 32<<20+1))
	if err != nil || n > 32<<20 {
		return errors.New("core_ruleset_copy_failed")
	}
	if hex.EncodeToString(h.Sum(nil)) != digest {
		return errors.New("core_ruleset_hash_mismatch")
	}
	if err = target.Sync(); err != nil {
		return errors.New("core_ruleset_copy_failed")
	}
	if err = target.Close(); err != nil {
		return errors.New("core_ruleset_copy_failed")
	}
	if err = os.Rename(target.Name(), filepath.Join(destination, name)); err != nil {
		return errors.New("core_ruleset_copy_failed")
	}
	return nil
}
