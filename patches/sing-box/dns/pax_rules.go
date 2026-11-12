package dns

import (
	"errors"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	"sync"
)

// A query retains its immutable rule table until its final synchronous/async result.
// Retired rule resources close on the last release, without a worker or polling.
type paxDNSLease struct {
	rules   []adapter.DNSRule
	readers int
	retired bool
}

func (r *Router) acquirePaxRules() ([]adapter.DNSRule, bool, func(), error) {
	r.rulesAccess.Lock()
	defer r.rulesAccess.Unlock()
	if r.closing {
		return nil, false, nil, errors.New("dns router closed")
	}
	if r.paxLease == nil {
		r.paxLease = &paxDNSLease{rules: r.rules}
	}
	lease := r.paxLease
	lease.readers++
	var once sync.Once
	return lease.rules, r.legacyDNSMode, func() {
		once.Do(func() {
			r.rulesAccess.Lock()
			lease.readers--
			closeNow := lease.retired && lease.readers == 0
			r.rulesAccess.Unlock()
			if closeNow {
				closeRules(lease.rules)
			}
		})
	}, nil
}
func (r *Router) closePaxRules() {
	r.rulesAccess.Lock()
	r.closing = true
	lease := r.paxLease
	if lease == nil {
		lease = &paxDNSLease{rules: r.rules}
	}
	lease.retired = true
	closeNow := lease.readers == 0
	r.rules = nil
	r.rulesAccess.Unlock()
	if closeNow {
		closeRules(lease.rules)
	}
}

// ReplaceRules retains the native DNS router, transports and their TTL cache.
func (r *Router) ReplaceRules(options []option.DNSRule) error {
	r.rulesAccess.Lock()
	if r.closing || !r.started {
		r.rulesAccess.Unlock()
		return errors.New("dns router unavailable")
	}
	for _, rule := range options {
		if err := r.validatePaxDNSReferences(rule); err != nil {
			r.rulesAccess.Unlock()
			return err
		}
	}
	oldRaw := r.rawRules
	r.rawRules = append([]option.DNSRule(nil), options...)
	rules, legacy, _, err := r.buildRules(true)
	if err != nil {
		r.rawRules = oldRaw
		r.rulesAccess.Unlock()
		return err
	}
	old := r.paxLease
	if old == nil {
		old = &paxDNSLease{rules: r.rules}
	}
	old.retired = true
	closeNow := old.readers == 0
	r.rules = rules
	r.legacyDNSMode = legacy
	r.paxLease = &paxDNSLease{rules: rules}
	r.rulesAccess.Unlock()
	if closeNow {
		closeRules(old.rules)
	}
	r.ClearCache()
	return nil
}

func (r *Router) validatePaxDNSReferences(rule option.DNSRule) error {
	action := rule.DefaultOptions.DNSRuleAction
	if rule.Type == "logical" {
		action = rule.LogicalOptions.DNSRuleAction
		for _, child := range rule.LogicalOptions.Rules {
			if err := r.validatePaxDNSReferences(child); err != nil {
				return err
			}
		}
	}
	server := action.RouteOptions.Server
	if action.Action == "evaluate" {
		server = action.EvaluateOptions.Server
	}
	if server != "" {
		if _, ok := r.transport.Transport(server); !ok {
			return errors.New("dns transport unavailable")
		}
	}
	return nil
}
