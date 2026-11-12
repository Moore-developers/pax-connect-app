package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// ErrInvalidProfile intentionally excludes parser text which could contain credentials.
var ErrInvalidProfile = errors.New("invalid debug profile")

// ErrProfileUnavailable represents a missing or unsafe local credential file.
var ErrProfileUnavailable = errors.New("debug profile unavailable")

// DebugProfilePath is the only production source until import is implemented.
const DebugProfilePath = "/Library/Application Support/Pax Connect/debug-profile.json"

// ParseDebugProfile validates the supported private schema without exposing raw errors.
func ParseDebugProfile(data []byte) (DebugProfile, error) {
	var profile DebugProfile
	if len(data) > 16*1024 {
		return profile, ErrInvalidProfile
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&profile); err != nil {
		return DebugProfile{}, ErrInvalidProfile
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return DebugProfile{}, ErrInvalidProfile
	}
	if profile.ProtocolName == "hysteria2" {
		if profile.Transport == "" {
			profile.Transport = "quic"
		}
		if profile.Security == "" {
			profile.Security = "tls"
		}
	}
	if err := ValidateRoute(profile); err != nil {
		return DebugProfile{}, ErrInvalidProfile
	}

	return profile, nil
}

// LoadDebugProfile refuses links and non-root writable credentials before reading.
func LoadDebugProfile() (DebugProfile, error) {
	parent, err := os.Lstat(filepath.Dir(DebugProfilePath))
	if err != nil || !parent.IsDir() || parent.Mode().Perm() != 0700 || parent.Sys().(*syscall.Stat_t).Uid != 0 || parent.Sys().(*syscall.Stat_t).Gid != 0 {
		return DebugProfile{}, ErrProfileUnavailable
	}
	fd, err := syscall.Open(DebugProfilePath, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return DebugProfile{}, ErrProfileUnavailable
	}
	file := os.NewFile(uintptr(fd), "debug-profile")
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 16*1024 {
		return DebugProfile{}, ErrProfileUnavailable
	}
	stat := info.Sys().(*syscall.Stat_t)
	if stat.Uid != 0 || stat.Gid != 0 {
		return DebugProfile{}, ErrProfileUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(file, 16*1024+1))
	if err != nil {
		return DebugProfile{}, ErrProfileUnavailable
	}
	return ParseDebugProfile(data)
}
