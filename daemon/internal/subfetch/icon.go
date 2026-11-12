package subfetch

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// FetchIcon gets only an anonymous HTTPS favicon from one origin. The existing
// validated literal-IP dial path is reused; no system proxy, cookies or credentials.
func (f *Fetcher) FetchIcon(ctx context.Context, host string) ([]byte, error) {
	if transport, ok := f.client.Transport.(interface{ CloseIdleConnections() }); ok {
		defer transport.CloseIdleConnections()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	target := &url.URL{Scheme: "https", Host: host, Path: "/favicon.ico"}
	if err := f.checkURL(ctx, target); err != nil {
		return nil, err
	}
	client := *f.client
	client.Timeout = 5 * time.Second
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 2 || req.URL.Scheme != "https" || req.URL.Host != host || req.URL.User != nil {
			return ErrRedirects
		}
		req.Header.Del("Referer")
		req.Header.Del("Cookie")
		req.Header.Del("Authorization")
		return f.checkURL(ctx, req.URL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, ErrInvalidURL
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "image/png,image/x-icon,image/jpeg,image/webp")
	response, err := client.Do(req)
	if err != nil {
		return nil, ErrFailed
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, ErrHTTP
	}
	content := strings.ToLower(response.Header.Get("Content-Type"))
	if strings.Contains(content, "svg") || strings.Contains(content, "text/") {
		return nil, ErrFailed
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return nil, ErrFailed
	}
	if len(raw) > 1<<20 {
		return nil, ErrTooLarge
	}
	return raw, nil
}
