package singbox

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/miekg/dns"
	"github.com/sagernet/sing-box/adapter"
	boxdns "github.com/sagernet/sing-box/dns"
	"pax-connect/daemon/internal/config"
	"strings"
	"testing"
	"time"
)

type fakePrivacyDNS struct {
	boxdns.TransportAdapter
	calls int
	fail  bool
	rcode int
}

func (f *fakePrivacyDNS) Start(adapter.StartStage, *adapter.Scope) error { return nil }
func (f *fakePrivacyDNS) Reset()                                         {}
func (f *fakePrivacyDNS) Exchange(ctx context.Context, q *dns.Msg) (*dns.Msg, error) {
	f.calls++
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if f.fail {
		return nil, errors.New("transport_failed")
	}
	v := new(dns.Msg)
	v.SetReply(q)
	v.Rcode = f.rcode
	return v, nil
}
func (f *fakePrivacyDNS) ExchangeAsync(ctx context.Context, q *dns.Msg, cb func(*dns.Msg, error)) {
	cb(f.Exchange(ctx, q))
}
func TestEncryptedDNSFallbackKeepsNXDOMAINAndCancellation(t *testing.T) {
	q := new(dns.Msg)
	q.SetQuestion("fixture.test.", dns.TypeA)
	first, second := &fakePrivacyDNS{fail: true}, &fakePrivacyDNS{}
	pool := &encryptedPool{primary: first, backup: second}
	if _, e := pool.Exchange(context.Background(), q); e != nil || first.calls != 1 || second.calls != 1 {
		t.Fatal("encrypted backup not used", e)
	}
	if _, e := pool.Exchange(context.Background(), q); e != nil || first.calls != 1 || second.calls != 2 {
		t.Fatal("failed primary not cooled")
	}
	first.fail = false
	first.rcode = dns.RcodeNameError
	pool.primaryRetry = time.Time{}
	r, e := pool.Exchange(context.Background(), q)
	if e != nil || r.Rcode != dns.RcodeNameError || second.calls != 2 {
		t.Fatal("NXDOMAIN escaped to another resolver")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := second.calls
	if _, e = pool.Exchange(ctx, q); e == nil || second.calls != before {
		t.Fatal("cancelled query reached backup")
	}
}
func TestGeneratedDNSPrivacyIsEncryptedAndModesDiffer(t *testing.T) {
	p := testProfile()
	p.NetworkPrivacy = config.DefaultNetworkPrivacy()
	for _, mode := range []string{"balanced", "strict"} {
		p.NetworkPrivacy.DNSMode = mode
		options, e := dnsPolicy(p, true)
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(options)
		var doc map[string]any
		_ = json.Unmarshal(raw, &doc)
		geo := false
		for _, v := range doc["rules"].([]any) {
			if _, ok := v.(map[string]any)["rule_set"]; ok && v.(map[string]any)["server"] == "dns-domestic" {
				geo = true
			}
		}
		if geo != (mode == "balanced") {
			t.Fatal("strict mode still sends Geo DNS direct")
		}
		for _, v := range doc["servers"].([]any) {
			server := v.(map[string]any)
			if server["tag"] == "dns-intranet" {
				continue
			}
			if server["type"] != "https" || server["tls"].(map[string]any)["enabled"] != true {
				t.Fatal("plaintext DNS generated")
			}
		}
	}
}

func TestEncryptedDNSPoolEnvironmentAndSanitizedFailure(t *testing.T) {
	first, second := &fakePrivacyDNS{fail: true}, &fakePrivacyDNS{fail: true}
	p := &encryptedPool{primary: first, backup: second, pool: "overseas", outbound: "proxy"}
	before := p.Environment()[1]
	p.Reset()
	if p.Environment()[1] == before {
		t.Fatal("route reset did not invalidate native cache environment")
	}
	q := new(dns.Msg)
	q.SetQuestion("fixture.test.", dns.TypeA)
	for i := 0; i < 2; i++ {
		_, err := p.Exchange(context.Background(), q)
		if err == nil || !strings.Contains(err.Error(), "pool=overseas outbound=proxy") || !strings.Contains(err.Error(), "backup=transport") {
			t.Fatal("missing sanitized pool/path/stage", err)
		}
	}
}
