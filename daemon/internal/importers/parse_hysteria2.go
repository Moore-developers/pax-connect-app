package importers

import (
	"errors"
	"net/url"
	"pax-connect/daemon/internal/config"
)

func parseHY2(u *url.URL) (config.RouteConfig, error) {
	p, err := commonURI(u, "hysteria2")
	if err != nil {
		return p, err
	}
	q := u.Query()
	if err := checkQuery(q, "sni,alpn,insecure,allowInsecure,obfs,obfs-password"); err != nil {
		return p, err
	}
	p.Password = u.User.Username()
	if password, ok := u.User.Password(); ok {
		p.Password = p.Password + ":" + password
	}
	p.Transport = "quic"
	p.Security = "tls"
	applyTLS(&p, q)
	if q.Get("obfs") != "" && q.Get("obfs") != "salamander" {
		return p, errors.New("unsupported_field")
	}
	p.ObfsPassword = q.Get("obfs-password")
	return p, nil
}
