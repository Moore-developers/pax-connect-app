package config

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"pax-connect/daemon/internal/storage"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

var ErrRoutingLibrary = errors.New("routing_library_invalid")
var ErrRoutingConflict = errors.New("routing_revision_conflict")

// RoutingEntry is a product match, independent of kernel types. Imported Match is private.
type RoutingEntry struct {
	ID          string          `json:"id"`
	Method      string          `json:"method"`
	Target      string          `json:"target"`
	Destination string          `json:"destination"`
	Enabled     bool            `json:"enabled"`
	Match       json.RawMessage `json:"match,omitempty"`
}

// RoutingPackage owns ordered OR matches and, except priority rules, one destination.
type RoutingPackage struct {
	ID, Name, Kind, Destination, Source, Version string
	Enabled, Automatic                           bool
	Rules                                        []RoutingEntry
	PreviousRules                                []RoutingEntry `json:",omitempty"`
	PreviousVersion                              string         `json:",omitempty"`
	LastChecked                                  int64
	GeoContent                                   []RoutingEntry `json:",omitempty"`
}

// RoutingLibrary is the durable, revisioned local rule configuration.
type RoutingLibrary struct {
	WebRTC   bool `json:"-"`
	Schema   int
	Revision uint64
	Packages []RoutingPackage
}

// RoutingApplication freezes a confirmed executable identity, including bundle-contained helpers.
type RoutingApplication struct {
	ID, Name, Path string
	Bundle         bool
	Domains        []string
}

func NewRoutingLibrary() RoutingLibrary {
	return RoutingLibrary{Schema: 1, Revision: 1, Packages: []RoutingPackage{
		{ID: "priority", Name: "优先规则", Kind: "priority", Enabled: true, Destination: "global", Rules: []RoutingEntry{}},
		{ID: "geo", Name: "Geo 基础包", Kind: "geo", Enabled: true, Destination: "direct", Version: "随 App 提供", Rules: GeoBuiltinRules()},
	}}
}

// GeoBuiltinRules describes independent built-in destinations; the package itself is mixed.
func GeoBuiltinRules() []RoutingEntry {
	return []RoutingEntry{
		{ID: "geo-private", Method: "builtin", Target: "局域网与私有网络地址", Destination: "direct", Enabled: true},
		{ID: "geosite-google", Method: "builtin", Target: "Google 服务（GeoSite Google）", Destination: "global", Enabled: true},
		{ID: "geosite-cn", Method: "builtin", Target: "中国域名（GeoSite CN）", Destination: "direct", Enabled: true},
		{ID: "geoip-cn", Method: "builtin", Target: "中国 IP（GeoIP CN）", Destination: "direct", Enabled: true},
	}
}
func (l RoutingLibrary) Clone() RoutingLibrary {
	data, _ := json.Marshal(l)
	var next RoutingLibrary
	_ = json.Unmarshal(data, &next)
	return next
}
func (l RoutingLibrary) Validate() error {
	if l.Schema != 1 || l.Revision == 0 || len(l.Packages) < 2 || len(l.Packages) > 128 || l.Packages[0].ID != "priority" || l.Packages[len(l.Packages)-1].ID != "geo" {
		return ErrRoutingLibrary
	}
	presentationBytes := 0
	seen, names := map[string]bool{}, map[string]bool{}
	total := 0
	for _, p := range l.Packages {
		if p.ID == "" || len(p.ID) > 128 || seen[p.ID] || p.Name == "" || len([]rune(p.Name)) > 48 || (names[p.Name] && p.Kind != "adblock") || !routingDestination(p.Destination, false) {
			return ErrRoutingLibrary
		}
		seen[p.ID] = true
		if p.Kind != "adblock" {
			names[p.Name] = true
		}
		if p.Kind == "geo" {
			for _, r := range p.GeoContent {
				if r.Method != "source" || len(r.Match) > 2<<20 || !json.Valid(r.Match) {
					return ErrRoutingLibrary
				}
			}
		}
		switch p.Kind {
		case "priority":
			if p.ID != "priority" || !p.Enabled {
				return ErrRoutingLibrary
			}
		case "geo":
			if p.ID != "geo" {
				return ErrRoutingLibrary
			}
		case "adblock":
			if p.ID != AdblockPackageID || p.Name != "广告拦截" || p.Source != AdblockSource || p.Destination != "block" || l.Packages[len(l.Packages)-2].ID != p.ID {
				return ErrRoutingLibrary
			}
		case "local":
			if p.ID == "priority" || p.ID == "geo" || p.ID == AdblockPackageID || p.Source != "" {
				return ErrRoutingLibrary
			}
		case "subscription":
			if p.ID == AdblockPackageID || !RoutingSourceValid(p.Source) {
				return ErrRoutingLibrary
			}
		default:
			return ErrRoutingLibrary
		}
		ids, matches := map[string]bool{}, map[string]bool{}
		for _, r := range p.Rules {
			presentationBytes += len(r.ID) + len(r.Target) + len(r.Method) + len(r.Destination) + 32
		}
		if presentationBytes > 2<<20 {
			return ErrRoutingLibrary
		}
		for _, r := range append(slices.Clone(p.Rules), p.PreviousRules...) {
			total++
			if total > 20000 || r.ID == "" || len(r.ID) > 128 || len(r.Target) > 4096 || !utf8.ValidString(r.Target) || !routingDestination(r.Destination, p.Kind == "priority") {
				return ErrRoutingLibrary
			}
			if p.Kind == "geo" {
				if r.Method != "builtin" {
					return ErrRoutingLibrary
				}
				continue
			}
			if p.CanUpdate() {
				if r.Method != "source" || len(r.Match) == 0 || len(r.Match) > 1<<20 || !json.Valid(r.Match) {
					return ErrRoutingLibrary
				}
				continue
			}
			normalized, err := NormalizeRoutingTarget(r.Method, r.Target)
			if err != nil || normalized != r.Target || ids[r.ID] || matches[r.Method+"|"+r.Target] || len(r.Match) > 0 {
				return ErrRoutingLibrary
			}
			ids[r.ID] = true
			matches[r.Method+"|"+r.Target] = true
		}
	}
	return nil
}
func routingDestination(v string, priority bool) bool {
	return v == "direct" || v == "global" || v == "block" || (priority && v == "default") || (strings.HasPrefix(v, "route:") && len(v) > 6 && len(v) <= 134)
}
func RoutingSourceValid(v string) bool {
	u, e := url.Parse(v)
	return e == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && len(v) <= 2048 && !strings.ContainsAny(v, " \t\r\n")
}

var routingDomain = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func NormalizeRoutingTarget(method, target string) (string, error) {
	value := strings.TrimSpace(target)
	switch method {
	case "application":
		if value != "" && len(value) <= 4096 && !strings.ContainsAny(value, "\x00\r\n") {
			return value, nil
		}
	case "domain", "domain_suffix":
		value = strings.TrimSuffix(strings.ToLower(value), ".")
		if len(value) <= 253 && strings.Contains(value, ".") {
			valid := true
			for _, part := range strings.Split(value, ".") {
				valid = valid && routingDomain.MatchString(part)
			}
			if valid {
				return value, nil
			}
		}
	case "ip":
		if ip, e := netip.ParseAddr(value); e == nil {
			return ip.String(), nil
		}
	case "cidr":
		if ip, e := netip.ParsePrefix(value); e == nil {
			return ip.Masked().String(), nil
		}
	}
	return "", ErrRoutingLibrary
}

// RoutingLibraryStore uses the protected atomic file shell; corrupt files are never replaced.
type RoutingLibraryStore struct{ file *storage.PrivateFile }

func NewRoutingLibraryStore(path string, owner uint32) *RoutingLibraryStore {
	return &RoutingLibraryStore{storage.NewPrivateFile(path, owner, 8<<20)}
}
func (s *RoutingLibraryStore) Load() (RoutingLibrary, bool, error) {
	raw, exists, err := s.file.Read()
	if err != nil || !exists {
		return NewRoutingLibrary(), exists, err
	}
	var l RoutingLibrary
	if json.Unmarshal(raw, &l) != nil || l.Validate() != nil {
		return l, true, ErrRoutingLibrary
	}
	return l, true, nil
}
func (s *RoutingLibraryStore) Save(ctx context.Context, l RoutingLibrary) error {
	if l.Validate() != nil {
		return ErrRoutingLibrary
	}
	raw, err := json.Marshal(l)
	if err != nil {
		return ErrRoutingLibrary
	}
	return s.file.Write(ctx, raw)
}
