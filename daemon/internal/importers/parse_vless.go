package importers

import (
	"errors"
	"net/url"
	"pax-connect/daemon/internal/config"
)

func parseVLESS(u *url.URL) (config.RouteConfig, error) {
	p, err := commonURI(u, "vless")
	if err != nil {
		return p, err
	}
	q := u.Query()
	if q.Get("type") == "xhttp" {
		return p, errors.New("unsupported_transport_xhttp")
	}
	if q.Get("mode") != "" && (q.Get("type") != "ws" && q.Get("type") != "httpupgrade" || q.Get("mode") != "multi" && q.Get("mode") != "gun") {
		return p, errors.New("unsupported_field")
	}
	if err := checkQuery(q, "type,security,sni,host,path,fp,pbk,sid,flow,alpn,insecure,allowInsecure,encryption,headerType,mode"); err != nil {
		return p, err
	}
	if q.Get("encryption") != "" && q.Get("encryption") != "none" || q.Get("headerType") != "" && q.Get("headerType") != "none" {
		return p, errors.New("unsupported_field")
	}
	p.UUID = u.User.Username()
	p.Transport = q.Get("type")
	if p.Transport == "" {
		p.Transport = "tcp"
	}
	p.Security = q.Get("security")
	if p.Security == "" {
		p.Security = "none"
	}
	if p.Transport == "xhttp" {
		return p, errors.New("unsupported_transport")
	}
	applyTLS(&p, q)
	p.WSPath = q.Get("path")
	if p.WSPath == "" {
		p.WSPath = "/"
	}
	p.WSHost = q.Get("host")
	p.Flow = q.Get("flow")
	p.RealityPublicKey = q.Get("pbk")
	p.RealityShortID = q.Get("sid")
	if p.Security == "reality" && p.Fingerprint == "" {
		p.Fingerprint = "chrome"
	}
	return p, nil
}
