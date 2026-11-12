package main

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Model metadata follows the user's existing fixed macOS proxy settings.
// It never changes the system proxy or uses root core/network measurement routes.
func aiModelHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = func(req *http.Request) (*url.URL, error) {
		if fromEnv, e := http.ProxyFromEnvironment(req); e != nil || fromEnv != nil {
			return fromEnv, e
		}
		if host := req.URL.Hostname(); host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback() {
			return nil, nil
		}
		ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
		defer cancel()
		raw, e := exec.CommandContext(ctx, "/usr/sbin/scutil", "--proxy").Output()
		if e != nil {
			return nil, nil
		}
		values := map[string]string{}
		scanner := bufio.NewScanner(strings.NewReader(string(raw)))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.Contains(line, "<dictionary>") && len(values) > 0 {
				break
			}
			if k, v, ok := strings.Cut(line, " : "); ok {
				values[k] = v
			}
		}
		kind := "HTTPS"
		if req.URL.Scheme == "http" {
			kind = "HTTP"
		}
		if values[kind+"Enable"] != "1" {
			return nil, nil
		}
		port, e := strconv.Atoi(values[kind+"Port"])
		host := values[kind+"Proxy"]
		if e != nil || port < 1 || port > 65535 || host == "" || strings.ContainsAny(host, "/@?#\\\r\n ") {
			return nil, nil
		}
		return &url.URL{Scheme: "http", Host: net.JoinHostPort(host, strconv.Itoa(port))}, nil
	}
	transport.TLSHandshakeTimeout = 8 * time.Second
	transport.ResponseHeaderTimeout = 12 * time.Second
	return &http.Client{Transport: transport, Timeout: 20 * time.Second}
}
