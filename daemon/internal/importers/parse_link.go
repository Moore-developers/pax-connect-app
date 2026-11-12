package importers

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"pax-connect/daemon/internal/config"
)

var parsers = map[string]func(*url.URL) (config.RouteConfig, error){"vless": parseVLESS, "hysteria2": parseHY2, "hy2": parseHY2, "trojan": parseTrojan, "anytls": parseAnyTLS, "ss": parseShadowsocks}

// ParseLink parses exactly one share link without I/O, persistence or kernel side effects.
func ParseLink(raw string) (config.RouteConfig, error) {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
	if len(raw) > 32*1024 || strings.ContainsAny(raw, "\n\r") {
		return config.RouteConfig{}, errors.New("invalid_link")
	}
	var p config.RouteConfig
	var err error
	if strings.HasPrefix(strings.ToLower(raw), "vmess://") {
		p, err = parseVMess(raw[len("vmess://"):])
	} else {
		u, e := url.Parse(raw)
		if e != nil || u.User == nil || u.Hostname() == "" {
			return p, errors.New("invalid_link")
		}
		parser, ok := parsers[strings.ToLower(u.Scheme)]
		if !ok {
			return p, errors.New("unsupported_protocol")
		}
		q, e := url.ParseQuery(u.RawQuery)
		if e != nil {
			return p, errors.New("invalid_link")
		}
		for _, values := range q {
			for _, value := range values {
				if value != values[0] {
					return p, errors.New("conflicting_parameter")
				}
			}
		}
		p, err = parser(u)
	}
	if err != nil {
		return config.RouteConfig{}, err
	}
	if err := config.ValidateRoute(p); err != nil {
		return config.RouteConfig{}, err
	}
	return p, nil
}
func decodeBase64(raw string) ([]byte, error) {
	raw = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, raw)
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if data, err := encoding.DecodeString(raw); err == nil {
			return data, nil
		}
	}
	return nil, errors.New("invalid_base64")
}
func commonURI(u *url.URL, protocol string) (config.RouteConfig, error) {
	port := 443
	if u.Port() != "" {
		v, err := strconv.ParseUint(u.Port(), 10, 16)
		if err != nil || v == 0 {
			return config.RouteConfig{}, errors.New("invalid_port")
		}
		port = int(v)
	}
	name := u.Fragment
	if name == "" {
		name = protocol + "线路"
	}
	return config.RouteConfig{NodeProfile: config.NodeProfile{Name: name, ProtocolName: protocol, Server: u.Hostname(), Port: uint16(port)}, SchemaVersion: 1}, nil
}
func checkQuery(q url.Values, allowed string) error {
	for key := range q {
		if !strings.Contains(","+allowed+",", ","+key+",") {
			return errors.New("unsupported_field")
		}
	}
	return nil
}
func applyTLS(p *config.RouteConfig, q url.Values) {
	p.ServerName = q.Get("sni")
	if p.ServerName == "" {
		p.ServerName = p.Server
	}
	p.Fingerprint = q.Get("fp")
	if q.Get("alpn") != "" {
		p.ALPN = strings.Split(q.Get("alpn"), ",")
	}
	for _, key := range []string{"insecure", "allowInsecure"} {
		p.TLSSkipSuggested = p.TLSSkipSuggested || q.Get(key) == "1" || q.Get(key) == "true"
	}
}
