package singbox

import (
	"context"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	R "github.com/sagernet/sing-box/route/rule"
	"github.com/sagernet/sing/common/json"
	M "github.com/sagernet/sing/common/metadata"
	"pax-connect/daemon/internal/config"
)

// Public fixture addresses must not match the private-address rule. A
// mixed DNS candidate set may match before a different public candidate connects;
// an eventual remote IP alone cannot establish the original match inputs.
func TestGeoPrivateMatchUsesMatchTimeAddresses(t *testing.T) {
	library := config.NewRoutingLibrary()
	library.Packages[1].GeoContent = []config.RoutingEntry{{ID: "cn", Method: "source", Target: "geosite-cn", Enabled: true, Match: []byte(`{"domain_suffix":["fixture.test"]}`)}}
	compiled, names, err := config.CompileRoutingEvidence(library, nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(compiled[2])
	if err != nil {
		t.Fatal(err)
	}
	var options option.Rule
	if err = json.Unmarshal(raw, &options); err != nil {
		t.Fatal(err)
	}
	rule, err := R.NewRule(context.Background(), log.NewNOPFactory().Logger(), options, false)
	if err != nil {
		t.Fatal(err)
	}
	evidence, ok := config.RecordedRoutingEvidence(names[2])
	if !ok || evidence.Target != "局域网与私有网络地址" {
		t.Fatal("compiled private rule and label diverged", evidence)
	}
	for _, address := range []string{"93.184.216.34", "8.8.8.8", "1.1.1.1"} {
		metadata := adapter.InboundContext{Destination: M.ParseSocksaddrHostPort(address, 443)}
		if rule.Match(&metadata) {
			t.Fatalf("public destination matched private rule: %s", address)
		}
	}
	metadata := adapter.InboundContext{Destination: M.ParseSocksaddrHostPort("registry.fixture.test", 443), DestinationAddresses: []netip.Addr{netip.MustParseAddr("93.184.216.34")}}
	if rule.Match(&metadata) {
		t.Fatal("public DNS candidate matched private rule")
	}
	metadata.ResetRuleCache()
	metadata.DestinationAddresses = append(metadata.DestinationAddresses, netip.MustParseAddr("192.168.1.2"))
	if !rule.Match(&metadata) {
		t.Fatal("mixed DNS candidate set should match the recorded private condition")
	}
}
