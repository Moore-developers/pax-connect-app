package importers

import (
	"net/url"
	"pax-connect/daemon/internal/config"
	"strings"
)

func parseShadowsocks(u *url.URL) (config.RouteConfig, error) {
	p, err := commonURI(u, "shadowsocks")
	if err != nil {
		return p, err
	}
	if u.Port() == "" || (u.Path != "" && u.Path != "/") {
		return p, config.ErrRouteDocument
	}
	if err = checkQuery(u.Query(), ""); err != nil {
		return p, err
	}
	method := u.User.Username()
	password, plain := u.User.Password()
	if !plain {
		decoded, err := decodeBase64(method)
		if err != nil {
			return p, err
		}
		var ok bool
		method, password, ok = strings.Cut(string(decoded), ":")
		if !ok {
			return p, config.ErrRouteDocument
		}
	}
	p.Method = method
	p.Password = password
	p.Transport = "tcp"
	p.Security = "none"
	return p, nil
}
