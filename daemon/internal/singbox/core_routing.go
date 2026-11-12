package singbox

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/sagernet/sing-box/experimental/paxobs"
	"github.com/sagernet/sing-box/option"
	"reflect"
)

// UpdateRouting changes only matching for future connections. The instance, DNS,
// outbounds and existing sockets remain owned by the same StartedService.
func (r *CoreRuntime) UpdateRouting(ctx context.Context, profile string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if r.Status() != "STARTED" || r.routingProfile == "" {
		return errors.New("routing_runtime_unavailable")
	}
	if !sameRoutingStructure(r.routingProfile, profile) {
		return errors.New("routing_restart_required")
	}
	var opts option.Options
	if opts.UnmarshalJSONContext(r.ctx, []byte(profile)) != nil || opts.Route == nil {
		return ErrConfig
	}
	router, ok := r.current().Instance().Box().Router().(interface {
		ReplaceNamedRules([]option.Rule, []string) error
	})
	if !ok {
		return errors.New("routing_update_unavailable")
	}
	dnsRouter, ok := r.current().Instance().Box().DNSRouter().(interface{ ReplaceRules([]option.DNSRule) error })
	if !ok {
		return errors.New("routing_update_unavailable")
	}
	var old option.Options
	if old.UnmarshalJSONContext(r.ctx, []byte(r.routingProfile)) != nil {
		return ErrConfig
	}
	if opts.DNS == nil {
		opts.DNS = &option.DNSOptions{}
	}
	if old.DNS == nil {
		old.DNS = &option.DNSOptions{}
	}
	if err := dnsRouter.ReplaceRules(opts.DNS.Rules); err != nil {
		return ErrConfig
	}
	if err := router.ReplaceNamedRules(opts.Route.Rules, paxobs.RuleNames(r.ctx)); err != nil {
		if restore := dnsRouter.ReplaceRules(old.DNS.Rules); restore != nil {
			return errors.New("routing_recovery_required")
		}
		return ErrConfig
	}
	r.routingProfile = profile
	r.cancelCurrentPath(true)
	return nil
}
func sameRoutingStructure(a, b string) bool {
	strip := func(raw string) map[string]any {
		var doc map[string]any
		if json.Unmarshal([]byte(raw), &doc) != nil {
			return nil
		}
		route, ok := doc["route"].(map[string]any)
		if !ok {
			return nil
		}
		delete(route, "rules")
		if dns, ok := doc["dns"].(map[string]any); ok {
			delete(dns, "rules")
		}
		return doc
	}
	old, next := strip(a), strip(b)
	return old != nil && next != nil && reflect.DeepEqual(old, next)
}

// SetRoutingSources freezes bounded product labels for the next table construction.
func (r *CoreRuntime) SetRoutingSources(sources []string) { paxobs.SetRuleNames(r.ctx, sources) }
