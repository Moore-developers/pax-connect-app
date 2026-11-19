package importers

import (
	"errors"
	"net/url"
	"unicode/utf8"

	"pax-connect/daemon/internal/config"
)

func parseVLESS(u *url.URL) (config.RouteConfig, error) {
	p, err := commonURI(u, "vless")
	if err != nil {
		return p, err
	}
	q := u.Query()
	p.Transport = q.Get("type")
	allowed := "type,security,sni,fp,pbk,sid,flow,alpn,insecure,allowInsecure,encryption,headerType,mode"
	mode := q.Get("mode")
	switch p.Transport {
	case "xhttp", "splithttp":
		return p, errors.New("unsupported_transport_xhttp")
	case "grpc":
		if q.Has("mode") && mode != "gun" || u.Path != "" && u.Path != "/" {
			return p, config.ErrDocumentField
		}
		allowed += ",serviceName"
	default:
		if mode != "" && (p.Transport != "ws" && p.Transport != "httpupgrade" || mode != "multi" && mode != "gun") {
			return p, config.ErrDocumentField
		}
		allowed += ",host,path"
	}
	if err := checkQuery(q, allowed); err != nil {
		return p, err
	}
	if q.Get("encryption") != "" && q.Get("encryption") != "none" || q.Get("headerType") != "" && q.Get("headerType") != "none" {
		return p, config.ErrDocumentField
	}
	p.UUID = u.User.Username()
	if p.Transport == "" {
		p.Transport = "tcp"
	}
	p.Security = q.Get("security")
	if p.Security == "" {
		p.Security = "none"
	}
	applyTLS(&p, q)
	p.Flow = q.Get("flow")
	p.RealityPublicKey = q.Get("pbk")
	p.RealityShortID = q.Get("sid")
	if p.Security == "reality" && p.Fingerprint == "" {
		p.Fingerprint = "chrome"
	}
	if p.Transport == "grpc" {
		serviceName := q.Get("serviceName")
		if !utf8.ValidString(serviceName) {
			return p, config.ErrRouteDocument
		}
		out, err := config.ParseDocument(config.DocumentForRoute(p))
		if err != nil {
			return p, err
		}
		out["transport"] = map[string]any{"type": "grpc", "service_name": serviceName}
		return routeFromObject(out)
	}
	p.WSPath = q.Get("path")
	if p.WSPath == "" {
		p.WSPath = "/"
	}
	p.WSHost = q.Get("host")
	return p, nil
}
