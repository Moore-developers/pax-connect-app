package singbox

import (
	"context"
	"errors"
	"fmt"
	"github.com/miekg/dns"
	"github.com/sagernet/sing-box/adapter"
	boxdns "github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/dns/transport"
	"github.com/sagernet/sing-box/experimental/paxobs"
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
	gate, _ := ctx.Value(coreRouteGateKey{}).(*routeGate)
	return &encryptedPool{gate: gate, pool: pool, outbound: outbound, primaryServer: o.Server, backupServer: backup.Server, TransportAdapter: boxdns.NewTransportAdapterWithRemoteOptions("https", tag, o.RemoteDNSServerOptions), primary: primary, backup: secondary}, nil
}

type encryptedPool struct {
	boxdns.TransportAdapter
	gate                        *routeGate
	primary, backup             adapter.DNSTransport
	mu                          sync.Mutex
	primaryRetry                time.Time
	pool, outbound              string
	primaryServer, backupServer string
	environment                 atomic.Uint64
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
	values := []string{"pax-encrypted-pool", strconv.FormatUint(p.environment.Load()+1, 10)}
	if p.gate != nil {
		values = append(values, strconv.FormatUint(p.gate.environment(), 10))
	}
	return values
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
	if p.gate != nil {
		leased, release, err := p.gate.acquire(ctx, p.outbound, nil)
		if err != nil {
			return nil, err
		}
		defer release()
		ctx = leased
	}
	ctx, cancel := context.WithTimeout(ctx, dnsTotalBudget)
	defer cancel()
	p.mu.Lock()
	retry := p.primaryRetry
	p.mu.Unlock()
	if time.Now().Before(retry) {
		answer, err := p.exchange(ctx, q, true)
		return answer, p.failure("cooldown", err)
	}
	first, done := context.WithTimeout(ctx, dnsPrimaryBudget)
	answer, e := p.exchange(first, q, false)
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
	answer, other := p.exchange(ctx, q, true)
	if other != nil {
		return nil, p.failure(dnsFailureStage(e), other)
	}
	return answer, nil
}

// A response proves which transport actually served this query, including backup/cooldown.
func (p *encryptedPool) exchange(ctx context.Context, q *dns.Msg, backup bool) (*dns.Msg, error) {
	peer, server := p.primary, p.primaryServer
	if backup {
		peer, server = p.backup, p.backupServer
	}
	answer, err := peer.Exchange(ctx, q)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err == nil && answer != nil {
		paxobs.Emit(ctx, paxobs.Event{Stage: "dns_upstream", Network: "doh", IP: server, Outbound: p.outbound, Rule: p.pool})
	}
	return answer, err
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
