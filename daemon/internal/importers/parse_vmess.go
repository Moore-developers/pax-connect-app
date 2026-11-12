package importers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"pax-connect/daemon/internal/config"
	"strconv"
	"strings"
)

func parseVMess(raw string) (config.RouteConfig, error) {
	data, err := decodeBase64(raw)
	if err != nil {
		return config.RouteConfig{}, err
	}
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(data))
	if decoder.Decode(&fields) != nil {
		return config.RouteConfig{}, errors.New("invalid_vmess")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return config.RouteConfig{}, errors.New("invalid_vmess")
	}
	allowed := map[string]bool{}
	for _, key := range []string{"v", "ps", "add", "port", "id", "aid", "scy", "net", "type", "host", "path", "tls", "sni", "alpn", "fp", "allowInsecure", "insecure"} {
		allowed[key] = true
	}
	for key, raw := range fields {
		var scalar any
		if json.Unmarshal(raw, &scalar) != nil {
			return config.RouteConfig{}, errors.New("invalid_vmess")
		}
		switch scalar.(type) {
		case string, float64, bool:
		default:
			return config.RouteConfig{}, errors.New("invalid_vmess")
		}
		if !allowed[key] {
			return config.RouteConfig{}, errors.New("unsupported_field")
		}
	}
	get := func(key string) string {
		v := fields[key]
		var s string
		if json.Unmarshal(v, &s) == nil {
			return s
		}
		var number json.Number
		if json.Unmarshal(v, &number) == nil {
			return number.String()
		}
		return ""
	}
	port, err := strconv.ParseUint(get("port"), 10, 16)
	if err != nil {
		return config.RouteConfig{}, errors.New("invalid_port")
	}
	aid := 0
	if get("aid") != "" {
		aid, err = strconv.Atoi(get("aid"))
		if err != nil {
			return config.RouteConfig{}, errors.New("invalid_vmess")
		}
	}
	p := config.RouteConfig{NodeProfile: config.NodeProfile{Name: get("ps"), ProtocolName: "vmess", Server: get("add"), Port: uint16(port)}, SchemaVersion: 1, UUID: get("id"), AlterID: aid, VMessSecurity: get("scy"), Transport: get("net"), Security: get("tls"), ServerName: get("sni"), WSHost: get("host"), WSPath: get("path"), Fingerprint: get("fp")}
	if p.Name == "" {
		p.Name = "VMess线路"
	}
	if p.ServerName == "" {
		p.ServerName = p.Server
	}
	if p.Security == "" {
		p.Security = "none"
	}
	if p.WSPath == "" {
		p.WSPath = "/"
	}
	if p.VMessSecurity == "" {
		p.VMessSecurity = "auto"
	}
	if get("type") != "" && get("type") != "none" {
		return p, errors.New("unsupported_field")
	}
	if get("alpn") != "" {
		p.ALPN = strings.Split(get("alpn"), ",")
	}
	p.TLSSkipSuggested = get("allowInsecure") == "1" || get("allowInsecure") == "true" || get("insecure") == "1" || get("insecure") == "true"
	return p, nil
}
