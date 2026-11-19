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
	if err := checkQuery(q, "sni,alpn,fp,insecure,allowInsecure,udp"); err != nil {
		return p, err
	}
	p.Password = u.User.Username()
	if password, ok := u.User.Password(); ok {
		p.Password += ":" + password
	}
	p.Security = "tls"
	p.Transport = "tcp"
	applyTLS(&p, q)
	if q.Has("udp") {
		switch q.Get("udp") {
		case "0":
			out, err := config.ParseDocument(config.DocumentForRoute(p))
			if err != nil {
				return p, err
			}
			out["network"] = "tcp"
			return routeFromObject(out)
		case "1":
		default:
			return p, config.ErrDocumentField
		}
	}
	return p, nil
}
