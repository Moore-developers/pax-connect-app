package singbox

import (
	"context"
	"encoding/json"
	M "github.com/sagernet/sing/common/metadata"
	"net/netip"
	"path/filepath"
	"testing"

	"github.com/miekg/dns"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	R "github.com/sagernet/sing-box/route/rule"
	boxjson "github.com/sagernet/sing/common/json"
	"pax-connect/daemon/internal/config"
)

func TestDNSHotUpdateRejectsQueryWithoutReplacingInstance(t *testing.T) {
	r := NewCoreRuntime(context.Background())
	defer r.Close()
	base := `{"log":{"disabled":true},"outbounds":[{"type":"direct","tag":"direct"}],"dns":{"servers":[{"type":"https","tag":"dns-remote","server":"1.1.1.1","tls":{"enabled":true,"server_name":"cloudflare-dns.com"}}],"rules":[{"domain":["old.fixture.test"],"action":"reject"}]},"route":{"final":"direct","rules":[]}}`
	var original map[string]any
	_ = json.Unmarshal([]byte(base), &original)
	original["experimental"] = map[string]any{"cache_file": map[string]any{"enabled": true, "path": filepath.Join(t.TempDir(), "cache.db")}}
	frozen, _ := json.Marshal(original)
	base = string(frozen)
	if err := r.Apply(base); err != nil {
		t.Fatal(err)
	}
	instance := r.current().Instance()
	var next map[string]any
	_ = json.Unmarshal([]byte(base), &next)
	next["dns"].(map[string]any)["rules"] = []any{map[string]any{"domain": []string{"new.fixture.test"}, "action": "reject"}}
	raw, _ := json.Marshal(next)
	if err := r.UpdateRouting(context.Background(), string(raw)); err != nil {
		t.Fatal(err)
	}
	if r.current().Instance() != instance {
		t.Fatal("hot edit replaced TUN owner")
	}
	q := new(dns.Msg)
	q.SetQuestion("new.fixture.test.", dns.TypeA)
	answer, err := instance.Box().DNSRouter().Exchange(context.Background(), q, adapter.DNSQueryOptions{})
	if err != nil || answer.Rcode != dns.RcodeRefused {
		t.Fatal("new DNS rule not committed", answer, err)
	}
	// Invalid DNS candidate must leave the previous rejecting table active.
	next["dns"].(map[string]any)["rules"] = []any{map[string]any{"domain": []string{"bad.test"}, "server": "missing"}}
	raw, _ = json.Marshal(next)
	if r.UpdateRouting(context.Background(), string(raw)) == nil {
		t.Fatal("missing DNS transport accepted")
	}
	answer, err = instance.Box().DNSRouter().Exchange(context.Background(), q, adapter.DNSQueryOptions{})
	if err != nil || answer.Rcode != dns.RcodeRefused {
		t.Fatal("invalid edit lost old DNS policy", err)
	}
}
func TestNativeWebRTCMatcherKeepsPrivateTrafficOutsideEnforcement(t *testing.T) {
	l := config.NewRoutingLibrary()
	l.WebRTC = true
	rules, err := config.CompileRouting(l, nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	ctx := include.Context(context.Background())
	raw, _ := json.Marshal(rules[1])
	var opts option.Rule
	if err = boxjson.UnmarshalContext(ctx, raw, &opts); err != nil {
		t.Fatal(err)
	}
	matcher, err := R.NewRule(ctx, log.NewNOPFactory().Logger(), opts, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = matcher.Close() }()
	for _, sample := range []struct {
		ip, protocol string
		want         bool
	}{{"1.1.1.1", "stun", true}, {"192.168.1.2", "stun", false}, {"2001:4860:4860::8888", "stun", true}, {"fd00::1", "stun", false}, {"1.1.1.1", "quic", false}} {
		metadata := adapter.InboundContext{Protocol: sample.protocol}
		metadata.Destination = M.Socksaddr{Addr: netip.MustParseAddr(sample.ip), Port: 3478}
		if matcher.Match(&metadata) != sample.want {
			t.Fatal("STUN policy mismatch", sample)
		}
	}
}
