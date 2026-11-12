package importers

import (
	"net/url"
	"pax-connect/daemon/internal/config"
)

func parseAnyTLS(u *url.URL) (config.RouteConfig, error) {
	p, err := commonURI(u, "anytls")
	if err != nil {
		return p, err
	}
	q := u.Query()
	if err := checkQuery(q, "sni,alpn,fp,insecure,allowInsecure"); err != nil {
		return p, err
	}
	p.Password = u.User.Username()
	if password, ok := u.User.Password(); ok {
		p.Password += ":" + password
	}
	p.Security = "tls"
	p.Transport = "tcp"
	applyTLS(&p, q)
	return p, nil
}
