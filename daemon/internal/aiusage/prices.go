package aiusage

import (
	"math"
	"strings"
	"time"
)

const priceVersion = "2026-11-09-standard-global"

// syntheticModel marks records Claude Code writes itself; they never reach a provider.
const syntheticModel = "<synthetic>"

// Price units are USD / million tokens. Sources and tier semantics are recorded
// in tests/ai-usage/adjustment-source-notes.md; collection never fetches prices.
type tariff struct{ input, read, short, long, output float64 }

var tariffs = map[string]tariff{
	"claude-fable-5-2": {8, .2, 10, 16, 40},
	"gpt-6-astra":      {10, 1, 12.5, 25, 50}, "gpt-6.1-sol": {2, .1, 2.5, 5, 10}, "gpt-6.1-terra": {1, .05, 1.25, 2.5, 5},
	"claude-opus-5-5": {4, .2, 5, 8, 20}, "claude-sonnet-5-5": {2, .1, 2.5, 4, 10}, "claude-sonnet-5": {2, .2, 2.5, 4, 10}, "claude-haiku-5-5": {.1, .01, .125, .2, .5},
	"claude-sonnet-4-5-20250929": {3, .3, 3.75, 6, 15}, "claude-haiku-4-5-20251001": {1, .1, 1.25, 2, 5}, "claude-opus-4-1-20250805": {15, 1.5, 18.75, 30, 75},
}

// attribute gives the request the Profile in effect at its own time, then prices it.
// The multiplier is the Profile's value for the request's model at that time (the
// per-model one, else the default), unless a repricing override covers the request.
// DSH is read-only usage: no Profile, multiplier 1.
func (r *Request) attribute(a attributor) {
	at := Attribution{Multiplier: 1}
	if r.CLI != "dsh" && a.resolve != nil {
		at = a.resolve(r.CLI, time.UnixMilli(r.At))
	}
	// A process started before an observed route change may still use its old
	// credentials. Without request-level auth evidence, do not claim either route.
	if at.Since > 0 && (r.sourceStart == 0 || r.sourceStart < at.Since) {
		at = Attribution{ServiceSource: "unknown", Multiplier: 1}
	}
	if r.CLI == "codex" && at.ServiceSource == "official_account" && r.sourceProvider != "" && r.sourceProvider != "openai" {
		at = Attribution{ServiceSource: "unknown", Multiplier: 1}
	}
	if r.CLI != "dsh" && at.ServiceSource == "" && at.ProfileID == "" {
		at.ServiceSource = "unknown"
	}
	r.Details.ServiceSource = at.ServiceSource
	m := at.ForModel(r.Model)
	if r.CLI != "dsh" && at.ProfileID != "" {
		m = a.multiplier(at.ProfileID, r.Model, r.At, m)
	}
	r.ProfileID, r.ProfileName, r.Multiplier = at.ProfileID, at.ProfileName, m
	r.price()
}

// price fills every cost field from the official component prices and the request's
// multiplier. A cache write without a usable 5m/1h split is billed at the 5m rate.
// "<synthetic>" costs nothing. A model missing from the table costs 0 and carries no
// unit prices; the rebuild program lists such models so they get added here.
func (r *Request) price() {
	r.Cost = 0
	r.Costs = [4]int64{}
	r.InputPrice, r.OutputPrice = nil, nil
	r.Details.Prices = [4]*float64{}
	r.Details.CacheWritePrices = [2]*float64{}
	r.Details.Billed = [4]int64{}
	r.Details.PriceVersion = priceVersion
	if r.Model == syntheticModel {
		return
	}
	v, ok := tariffs[r.Model]
	if r.Model == "deepseek-flash" || r.Model == "deepseek-v4.1-flash" { // same model, two names in DSH logs
		v, ok = deepseekTariff(r.At), true
	}
	if !ok {
		return
	}
	if strings.HasPrefix(r.Model, "gpt-") && r.Input > 272000 {
		v.input *= 2
		v.read *= 2
		v.short *= 2
		v.long *= 2
		v.output *= 1.5
	}
	if r.Model == "claude-haiku-5-5" && r.Input > 100000 {
		v.input *= 5
		v.read *= 5
		v.short *= 5
		v.long *= 5
		v.output *= 5
	}
	r.Details.CacheWritePrices = [2]*float64{&v.short, &v.long}
	r.InputPrice = &v.input
	r.OutputPrice = &v.output
	write := v.short
	if short, long, split := r.writeSplit(); split {
		write = (float64(short)*v.short + float64(long)*v.long) / float64(r.Write)
	}
	r.Details.Prices = [4]*float64{&v.input, &v.read, &write, &v.output}
	tokens := [4]int64{max(0, r.Input-r.Read-r.Write), r.Read, r.Write, r.Output}
	var total float64
	for i, p := range r.Details.Prices {
		amount := float64(tokens[i]) * *p
		r.Costs[i] = int64(math.Round(amount))
		r.Details.Billed[i] = int64(math.Round(amount * r.Multiplier))
		total += amount
	}
	r.Cost = int64(math.Round(total * r.Multiplier))
}

// writeSplit returns the 5m/1h cache-write tokens when the log gives a split that adds up.
func (r *Request) writeSplit() (short, long int64, ok bool) {
	if r.Write == 0 || r.Details.WriteShort == nil && r.Details.WriteLong == nil {
		return 0, 0, false
	}
	if r.Details.WriteShort != nil {
		short = *r.Details.WriteShort
	}
	if r.Details.WriteLong != nil {
		long = *r.Details.WriteLong
	}
	return short, long, short >= 0 && long >= 0 && short+long == r.Write
}
