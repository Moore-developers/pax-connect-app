package dns

import (
	"github.com/sagernet/sing-box/adapter"
	"testing"
)

// Protect reader lifetime across a swap/close without relying on timing sleeps.
type paxClosingRule struct {
	adapter.DNSRule
	closed int
}

func (p *paxClosingRule) Close() error { p.closed++; return nil }
func TestPaxDNSRuleLeaseClosesOnlyAfterFinalReader(t *testing.T) {
	rule := &paxClosingRule{}
	r := &Router{rules: []adapter.DNSRule{rule}}
	_, _, release, err := r.acquirePaxRules()
	if err != nil {
		t.Fatal(err)
	}
	_, _, release2, err := r.acquirePaxRules()
	if err != nil {
		t.Fatal(err)
	}
	r.closePaxRules()
	if rule.closed != 0 {
		t.Fatal("closed active query rules")
	}
	release()
	release()
	if rule.closed != 0 {
		t.Fatal("closed before final reader")
	}
	release2()
	if rule.closed != 1 {
		t.Fatal("rule resource not closed exactly once")
	}
	if _, _, _, err = r.acquirePaxRules(); err == nil {
		t.Fatal("query entered closed router")
	}
}
