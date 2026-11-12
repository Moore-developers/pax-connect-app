package importers

import (
	"net/url"
	"pax-connect/daemon/internal/config"
)

func parseTrojan(u *url.URL) (config.RouteConfig, error) {
	p, err := commonURI(u, "trojan")
	if err != nil {
		return p, err
	}
	q := u.Query()
	if err = checkQuery(q, "sni,alpn,fp,insecure,allowInsecure,type,security,host,path"); err != nil {
		return p, err
	}
	if q.Get("type") != "" && q.Get("type") != "tcp" && q.Get("type") != "ws" && q.Get("type") != "httpupgrade" {
		return p, config.ErrDocumentField
	}
	if q.Get("security") != "" && q.Get("security") != "tls" {
		return p, config.ErrDocumentField
	}
	p.Password = u.User.Username()
	if password, ok := u.User.Password(); ok {
		p.Password += ":" + password
	}
	p.Transport = q.Get("type")
	if p.Transport == "" {
		p.Transport = "tcp"
	}
	p.WSHost = q.Get("host")
	p.WSPath = q.Get("path")
	if p.WSPath == "" {
		p.WSPath = "/"
	}
	p.Security = "tls"
	applyTLS(&p, q)
	return p, nil
}
