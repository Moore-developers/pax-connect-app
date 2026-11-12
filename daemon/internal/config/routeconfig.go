package config

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"regexp"
	"strings"
)

// RouteConfig is a private product connection model, never sent wholesale to the App.
// DebugProfile aliases it during the one-time migration from the S0 fixed route.
type RouteConfig struct {
	AddressFamilyState  string               `json:"-"`
	IPv4Only            bool                 `json:"-"`
	NetworkPrivacy      NetworkPrivacy       `json:"-"`
	DNSRouteTags        map[string]string    `json:"-"`
	Routing             *RoutingLibrary      `json:"-"`
	RoutingApplications []RoutingApplication `json:"-"`
	MeasurementNetwork  string               `json:"-"`
	Front               *RouteConfig         `json:"-"`
	MeasurementParallel int                  `json:"-"`
	MeasurementLatency  int                  `json:"-"`
	MeasurementExit     int                  `json:"-"`
	// ResolvedConfigRevision freezes the owner-resolved connection version; never persisted.
	ResolvedConfigRevision uint64          `json:"-"`
	Method                 string          `json:"method,omitempty"`
	Document               json.RawMessage `json:"document,omitempty"`
	NodeProfile
	SchemaVersion    int           `json:"schema_version"`
	Username         string        `json:"username,omitempty"`
	Password         string        `json:"password,omitempty"`
	UUID             string        `json:"uuid,omitempty"`
	AlterID          int           `json:"alter_id,omitempty"`
	VMessSecurity    string        `json:"vmess_security,omitempty"`
	ServerName       string        `json:"server_name,omitempty"`
	ObfsPassword     string        `json:"obfs_password,omitempty"`
	Transport        string        `json:"transport,omitempty"`
	Security         string        `json:"security,omitempty"`
	WSPath           string        `json:"ws_path,omitempty"`
	WSHost           string        `json:"ws_host,omitempty"`
	ALPN             []string      `json:"alpn,omitempty"`
	Fingerprint      string        `json:"fingerprint,omitempty"`
	RealityPublicKey string        `json:"reality_public_key,omitempty"`
	RealityShortID   string        `json:"reality_short_id,omitempty"`
	Flow             string        `json:"flow,omitempty"`
	TLSSkipSuggested bool          `json:"tls_skip_suggested,omitempty"`
	TLSResolved      TLSPolicyView `json:"-"`
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ValidateRoute covers only supported protocol shapes, not arbitrary kernel JSON.
func ValidateRoute(p RouteConfig) error {
	invalid := errors.New("route_invalid")
	if p.SchemaVersion != 1 || p.Name == "" || len(p.Name) > 128 || p.Server == "" || len(p.Server) > 253 || p.Port == 0 || strings.ContainsAny(p.Server, " /\t\n") || len(p.WSPath) > 2048 || len(p.Password) > 4096 {
		return invalid
	}
	if strings.Contains(p.Server, ":") {
		if _, err := netip.ParseAddr(p.Server); err != nil {
			return invalid
		}
	}
	switch p.ProtocolName {
	case "socks", "http":
		if p.Transport != "tcp" || (p.Username == "") != (p.Password == "") || len(p.Username) > 4096 || strings.ContainsAny(p.Username+p.Password, "\r\n") {
			return invalid
		}
		if p.ProtocolName == "socks" {
			if p.Security != "none" || len(p.Username) > 255 || len(p.Password) > 255 {
				return invalid
			}
		} else if strings.Contains(p.Username, ":") || (p.Security != "none" && p.Security != "tls") {
			return invalid
		}
	case "anytls":
		if p.Password == "" || p.Security != "tls" || p.Transport != "tcp" {
			return invalid
		}
	case "trojan":
		if p.Password == "" || p.Security != "tls" || (p.Transport != "tcp" && p.Transport != "ws" && p.Transport != "httpupgrade") {
			return invalid
		}
	case "shadowsocks":
		if p.Password == "" || p.Security != "none" || p.Transport != "tcp" || p.TLSSkipSuggested {
			return invalid
		}
		switch p.Method {
		case "aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305":
		case "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305":
			size := 32
			if p.Method == "2022-blake3-aes-128-gcm" {
				size = 16
			}
			for _, encoded := range strings.Split(p.Password, ":") {
				key, err := base64.StdEncoding.DecodeString(encoded)
				if err != nil || len(key) != size {
					return invalid
				}
			}
		default:
			return ErrDocumentField
		}
	case "hysteria2":
		if p.Password == "" || p.Security != "tls" || p.Transport != "quic" {
			return invalid
		}
	case "vmess":
		if !uuidPattern.MatchString(p.UUID) || (p.Transport != "ws" && p.Transport != "httpupgrade") || (p.Security != "tls" && p.Security != "none") || p.AlterID < 0 || p.AlterID > 65535 {
			return invalid
		}
	case "vless":
		if !uuidPattern.MatchString(p.UUID) {
			return invalid
		}
		switch p.Transport {
		case "ws", "httpupgrade":
			if p.Security != "tls" || p.Flow != "" {
				return invalid
			}
		case "tcp":
			if p.Security != "reality" || len(p.RealityPublicKey) != 43 || len(p.RealityShortID) > 16 || len(p.RealityShortID)%2 != 0 || (p.Flow != "" && p.Flow != "xtls-rprx-vision") {
				return invalid
			}
			if _, err := hex.DecodeString(p.RealityShortID); err != nil {
				return invalid
			}
			if key, err := base64.RawURLEncoding.DecodeString(p.RealityPublicKey); err != nil || len(key) != 32 {
				return invalid
			}

		default:
			return errors.New("unsupported_transport")
		}
	default:
		return errors.New("unsupported_protocol")
	}
	if p.Security != "none" && (p.ServerName == "" || len(p.ServerName) > 253) {
		return invalid
	}
	if p.Security == "reality" && p.TLSSkipSuggested {
		return errors.New("unsupported_field")
	}
	return nil
}
