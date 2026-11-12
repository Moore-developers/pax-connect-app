package singbox

import (
	"context"
	"errors"
	"fmt"
	"github.com/miekg/dns"
	"github.com/sagernet/sing-box/adapter"
	boxdns "github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/dns/transport"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const dnsPrimaryBudget = 1500 * time.Millisecond
const dnsTotalBudget = 3 * time.Second

// Only product-owned encrypted pools get a backup; arbitrary transports retain upstream behavior.
func newEncryptedPool(ctx context.Context, logger log.ContextLogger, tag string, o option.RemoteHTTPSDNSServerOptions) (adapter.DNSTransport, error) {
	primary, e := transport.NewHTTPS(ctx, logger, tag, o)
	if e != nil {
		return nil, e
	}
	if o.TLS == nil {
		return primary, nil
	}
	backup := o
	tls := *o.TLS
	backup.TLS = &tls
	switch {
	case o.Server == "223.5.5.5" && tls.ServerName == "dns.alidns.com":
		backup.Server = "119.29.29.29"
		tls.ServerName = "doh.pub"
	case o.Server == "1.1.1.1" && tls.ServerName == "cloudflare-dns.com":
		backup.Server = "8.8.8.8"
		tls.ServerName = "dns.google"
	default:
		return primary, nil
	}
	secondary, e := transport.NewHTTPS(ctx, logger, tag+"-backup", backup)
	if e != nil {
		return nil, e
	}
	pool := "overseas"
	if o.Server == "223.5.5.5" {
		pool = "domestic"
	}
	outbound := o.Detour
	if outbound == "" {
		outbound = "direct"
	}
	return &encryptedPool{pool: pool, outbound: outbound, TransportAdapter: boxdns.NewTransportAdapterWithRemoteOptions("https", tag, o.RemoteDNSServerOptions), primary: primary, backup: secondary}, nil
}

type encryptedPool struct {
	boxdns.TransportAdapter
	primary, backup adapter.DNSTransport
	mu              sync.Mutex
	primaryRetry    time.Time
	pool, outbound  string
	environment     atomic.Uint64
}

func (p *encryptedPool) Start(stage adapter.StartStage, s *adapter.Scope) error {
	if e := s.Start("primary encrypted DNS", p.primary, stage); e != nil {
		return e
	}
	return s.Start("backup encrypted DNS", p.backup, stage)
}

// Native cache keys include Environment, so in-flight replies from the previous
// route/network cannot repopulate the cache after a reset.
func (p *encryptedPool) Environment() []string {
	return []string{"pax-encrypted-pool", strconv.FormatUint(p.environment.Load()+1, 10)}
}
func (p *encryptedPool) Reset() {
	p.environment.Add(1)
	p.mu.Lock()
	p.primaryRetry = time.Time{}
	p.mu.Unlock()
	p.primary.Reset()
	p.backup.Reset()
}
func (p *encryptedPool) Exchange(ctx context.Context, q *dns.Msg) (*dns.Msg, error) {
	ctx, cancel := context.WithTimeout(ctx, dnsTotalBudget)
	defer cancel()
	p.mu.Lock()
	retry := p.primaryRetry
	p.mu.Unlock()
	if time.Now().Before(retry) {
		answer, err := p.backup.Exchange(ctx, q)
		return answer, p.failure("cooldown", err)
	}
	first, done := context.WithTimeout(ctx, dnsPrimaryBudget)
	answer, e := p.primary.Exchange(first, q)
	done()
	if e == nil {
		return answer, nil
	} // NXDOMAIN and DNS errors are answers, not transport fallback triggers.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	p.mu.Lock()
	p.primaryRetry = time.Now().Add(30 * time.Second)
	p.mu.Unlock()
	answer, other := p.backup.Exchange(ctx, q)
	if other != nil {
		return nil, p.failure(dnsFailureStage(e), other)
	}
	return answer, nil
}
func (p *encryptedPool) ExchangeAsync(ctx context.Context, q *dns.Msg, cb func(*dns.Msg, error)) {
	go func() { cb(p.Exchange(ctx, q)) }()
}

// Only bounded stage labels and product-owned tags cross this boundary, never
// HTTP response text, credentials, certificate subjects or private query data.
func (p *encryptedPool) failure(primary string, backup error) error {
	if backup == nil {
		return nil
	}
	return fmt.Errorf("encrypted_dns_pool_unavailable pool=%s outbound=%s primary=%s backup=%s", p.pool, p.outbound, primary, dnsFailureStage(backup))
}
func dnsFailureStage(err error) string {
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
		return "timeout"
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "tls") || strings.Contains(text, "certificate") {
		return "tls"
	}
	if strings.Contains(text, "unexpected status") || strings.Contains(text, "http") {
		return "http"
	}
	return "transport"
}
