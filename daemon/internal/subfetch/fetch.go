// Package subfetch 负责一次有界的 HTTPS 订阅取数。
//
// 负责什么：按产品策略校验目标、限制跳转与大小、在总 deadline 内返回响应正文；
// 下载失败一律返回产品错误码，不回显 URL、凭据或响应正文。
// 不负责什么：不解码、不解析分享链接（由 importers 负责），不写 catalog，不接触内核。
// 谁依赖它：controller 通过 SubscriptionFetcher 边界注入；测试用替身替换。
package subfetch

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const (
	// UserAgent identifies the product; providers may independently reject a source or account.
	UserAgent = "Pax-Connect/0.1"
	// Deadline 是一次订阅下载的总上限，含 DNS、跳转与正文读取。
	Deadline     = 25 * time.Second
	maxRedirects = 3
	maxBodyBytes = 2 * 1024 * 1024
)

// 产品错误码：UI 用 code 渲染文案，不拼接触底错误。
var (
	ErrDNS        = errors.New("fetch_dns_failed")
	ErrTLS        = errors.New("fetch_tls_failed")
	ErrTimeout    = errors.New("fetch_timeout")
	ErrForbidden  = errors.New("fetch_forbidden")
	ErrHTTP       = errors.New("fetch_http_failed")
	ErrConnection = errors.New("fetch_connection_failed")
	ErrScheme     = errors.New("fetch_scheme_unsupported")
	ErrTarget     = errors.New("fetch_target_disallowed")
	ErrRedirects  = errors.New("fetch_redirect_limit")
	ErrTooLarge   = errors.New("fetch_too_large")
	ErrFailed     = errors.New("fetch_failed")
	ErrCancelled  = errors.New("operation_cancelled")
	ErrInvalidURL = errors.New("fetch_url_invalid")
)

// Options allows substitutes in tests; the production constructor uses the system resolver.
type Options struct {
	Lookup    func(context.Context, string) ([]netip.Addr, error)
	Transport http.RoundTripper
	// DialContext receives only validated literal public destinations. It can be
	// bound to an isolated proxy route without importing that route's types.
	DialContext func(context.Context, string, string) (net.Conn, error)
}

// Fetcher downloads one subscription body under the bounded product policy.
type Fetcher struct {
	lookup  func(context.Context, string) ([]netip.Addr, error)
	client  *http.Client
	options Options
}

func New() *Fetcher { return NewWithOptions(Options{}) }

func NewWithOptions(options Options) *Fetcher {
	f := &Fetcher{lookup: options.Lookup, options: options}
	if f.lookup == nil {
		f.lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		}
	}
	transport := options.Transport
	if transport == nil {
		dial := options.DialContext
		if dial == nil {
			dial = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 15 * time.Second}).DialContext
		}
		transport = &http.Transport{
			Proxy:                 nil,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(address)
				if err != nil {
					return nil, ErrTarget
				}
				var addresses []netip.Addr
				if ip, e := netip.ParseAddr(host); e == nil {
					addresses = []netip.Addr{ip}
				} else {
					addresses, err = f.lookup(ctx, host)
					if err != nil {
						return nil, ErrDNS
					}
				}
				if len(addresses) == 0 {
					return nil, ErrDNS
				}
				for _, ip := range addresses {
					if !allowedAddr(ip) {
						return nil, ErrTarget
					}
				}
				return dialAddresses(ctx, dial, network, port, addresses)
			},
		}
	}
	f.client = &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return ErrRedirects
			}
			return f.checkURL(req.Context(), req.URL)
		},
	}
	return f
}

// FetchSubscription returns the bounded body of one HTTPS source.
func (f *Fetcher) FetchSubscription(ctx context.Context, raw string) ([]byte, error) {
	body, _, err := f.FetchSubscriptionRequest(ctx, raw, "")
	return body, err
}

func (f *Fetcher) fetch(ctx context.Context, raw, agent string, observe func(string, int, time.Duration)) ([]byte, error) {
	if len(agent) > 256 || strings.ContainsAny(agent, "\r\n") {
		return nil, ErrInvalidURL
	}
	parsed, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 {
		return nil, ErrInvalidURL
	}
	bounded, cancel := context.WithTimeout(ctx, Deadline)
	defer cancel()
	if err := f.checkURL(bounded, parsed); err != nil {
		if ctx.Err() != nil {
			return nil, ErrCancelled
		}
		if bounded.Err() != nil {
			return nil, ErrTimeout
		}
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	request, err := http.NewRequestWithContext(bounded, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, ErrInvalidURL
	}
	if agent == "" {
		agent = UserAgent
	}
	request.Header.Set("User-Agent", agent)
	response, err := f.client.Do(request)
	if err != nil {
		failure := classify(ctx, bounded, err)
		slog.Info("subscription fetch failed", "reason", failure.Error())
		return nil, failure
	}
	defer func() { _ = response.Body.Close() }()
	observe("response", response.StatusCode, 0)
	if response.StatusCode < 200 || response.StatusCode > 299 {
		slog.Info("subscription fetch rejected", "reason", "http_status", "status", response.StatusCode)
		if response.StatusCode == http.StatusForbidden {
			return nil, ErrForbidden
		}
		return nil, ErrHTTP
	}
	if response.ContentLength > maxBodyBytes {
		return nil, ErrTooLarge
	}
	observe("body", 0, 0)
	bodyStarted := time.Now()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	observe("body", 0, time.Since(bodyStarted))
	if err != nil {
		failure := classify(ctx, bounded, err)
		slog.Info("subscription fetch failed", "reason", failure.Error())
		return nil, failure
	}
	if len(body) > maxBodyBytes {
		return nil, ErrTooLarge
	}
	if len(body) == 0 {
		return nil, ErrFailed
	}
	return body, nil
}

// checkURL enforces HTTPS and a public, non-metadata destination before any connection.
func (f *Fetcher) checkURL(ctx context.Context, parsed *url.URL) error {
	if parsed.Scheme != "https" {
		return ErrScheme
	}
	host := parsed.Hostname()
	if host == "" {
		return ErrInvalidURL
	}
	return f.checkHost(ctx, host)
}

func (f *Fetcher) checkHost(ctx context.Context, host string) error {
	if literal, err := netip.ParseAddr(host); err == nil {
		if !allowedAddr(literal) {
			return ErrTarget
		}
		return nil
	}
	addresses, err := f.lookup(ctx, host)
	if err != nil {
		return ErrDNS
	}
	if len(addresses) == 0 {
		return ErrDNS
	}
	for _, address := range addresses {
		if !allowedAddr(address) {
			return ErrTarget
		}
	}
	return nil
}

// allowedAddr rejects loopback, private, link-local, multicast, unspecified and other
// special-purpose ranges, including the cloud metadata endpoints.
func allowedAddr(address netip.Addr) bool {
	if !address.IsValid() {
		return false
	}
	address = address.Unmap()
	if address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() || address.IsMulticast() || address.IsUnspecified() ||
		address.IsInterfaceLocalMulticast() {
		return false
	}
	for _, prefix := range specialPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

var specialPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),   // CGNAT
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // documentation
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // documentation
	netip.MustParsePrefix("203.0.113.0/24"),  // documentation
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
	netip.MustParsePrefix("64:ff9b::/96"),    // NAT64
}

func classify(parent, bounded context.Context, err error) error {
	if errors.Is(err, ErrRedirects) {
		return ErrRedirects
	}
	if errors.Is(err, ErrTarget) {
		return ErrTarget
	}
	if errors.Is(err, ErrScheme) {
		return ErrScheme
	}
	if parent.Err() != nil {
		return parent.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) || bounded.Err() != nil {
		return ErrTimeout
	}
	var invalid x509.CertificateInvalidError
	var unknown x509.UnknownAuthorityError
	var hostname x509.HostnameError
	if errors.As(err, &invalid) || errors.As(err, &unknown) || errors.As(err, &hostname) {
		return ErrTLS
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return ErrDNS
	}
	var operation *net.OpError
	if errors.As(err, &operation) {
		if operation.Timeout() {
			return ErrTimeout
		}
		return ErrConnection
	}
	if err != nil {
		var urlError *url.Error
		if errors.As(err, &urlError) {
			return ErrFailed
		}
	}
	return ErrFailed
}
