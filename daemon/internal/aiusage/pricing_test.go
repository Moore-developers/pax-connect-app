package aiusage

import (
	"testing"
	"time"
)

func TestOfficialComponentPricesRespectTTLAndMultiplier(t *testing.T) {
	r := Request{Model: "claude-sonnet-5-5", CLI: "claude", Input: 2000000, Read: 500000, Write: 500000, Output: 1000000, Multiplier: .26}
	short, long := int64(200000), int64(300000)
	r.Details.WriteShort = &short
	r.Details.WriteLong = &long
	r.price()
	if r.Cost != 3575000 || r.Costs != [4]int64{2000000, 50000, 1700000, 10000000} {
		t.Fatalf("official four prices: %+v", r)
	}
}

func TestCacheWriteWithoutATierIsBilledAtTheFiveMinuteRate(t *testing.T) {
	g := Request{Model: "gpt-6.1-sol", CLI: "codex", Input: 100000, Read: 50000, Write: 10000, Output: 1000, Multiplier: .16}
	g.price()
	// 40000*2 + 50000*.1 + 10000*2.5 + 1000*10 = 120000 micro-USD at 1x.
	if g.Costs != [4]int64{80000, 5000, 25000, 10000} || g.Cost != 19200 || g.Details.Prices[2] == nil || *g.Details.Prices[2] != 2.5 {
		t.Fatalf("missing TTL: %+v", g)
	}
	// A split that does not add up to the write count is no split either.
	bad := int64(1)
	g.Details.WriteShort = &bad
	g.price()
	if g.Costs[2] != 25000 {
		t.Fatalf("inconsistent TTL: %+v", g.Costs)
	}
	// Only a long tier is a split when it accounts for every written token.
	all := int64(10000)
	g.Details.WriteShort, g.Details.WriteLong = nil, &all
	g.price()
	if g.Costs[2] != 50000 {
		t.Fatalf("1h only: %+v", g.Costs)
	}
}

func TestFable52PricesTheFullContextAndBothCacheWriteTiers(t *testing.T) {
	// 300k uncached + 400k reads + 200k writes. Fable has no long-context premium.
	r := Request{Model: "claude-fable-5-2", CLI: "claude", Input: 900000, Read: 400000, Write: 200000, Output: 10000, Multiplier: .26}
	short, long := int64(80000), int64(120000)
	r.Details.WriteShort, r.Details.WriteLong = &short, &long
	r.price()
	// USD 2.40 + .08 + (0.80 + 1.92) + .40 = 5.60; at .26x = 1.456.
	if r.Costs != [4]int64{2400000, 80000, 2720000, 400000} || r.Cost != 1456000 || r.Details.Billed != [4]int64{624000, 20800, 707200, 104000} {
		t.Fatalf("Fable component billing: %+v", r)
	}
	if r.InputPrice == nil || *r.InputPrice != 8 || r.OutputPrice == nil || *r.OutputPrice != 40 || r.Details.Prices[1] == nil || *r.Details.Prices[1] != .2 || r.Details.CacheWritePrices[0] == nil || *r.Details.CacheWritePrices[0] != 10 || r.Details.CacheWritePrices[1] == nil || *r.Details.CacheWritePrices[1] != 16 || r.Details.PriceVersion != priceVersion {
		t.Fatalf("Fable prices for readers: %+v", r)
	}
}

func TestUltrafastAliasesUseSupportedOfficialRates(t *testing.T) {
	at := time.Date(2026, time.November, 19, 12, 0, 0, 0, time.UTC).UnixMilli()
	for _, tc := range []struct {
		model   string
		rates   [5]float64 // uncached input, cache read, 30m write, 12h write, output
		version string
	}{
		{"gpt-6-astra-ultrafast", [5]float64{60, 6, 75, 150, 300}, ultrafastPriceVersion},
		{"gpt-6.1-sol-ultrafast", [5]float64{12, .6, 15, 30, 60}, ultrafastPriceVersion},
		{"gpt-6.2-astra-ultrafast", [5]float64{24, 1.2, 30, 60, 120}, ultrafastPriceVersion},
		{"gpt-6.2-sol-ultrafast", [5]float64{6, .3, 7.5, 15, 30}, ultrafastPriceVersion},
		{"gpt-6.2-sol", [5]float64{2, .1, 2.5, 5, 10}, priceVersion},
	} {
		t.Run(tc.model, func(t *testing.T) {
			r := Request{Model: tc.model, CLI: "codex", At: at, Input: 272000, Multiplier: .16, Details: Details{ReasoningEffort: "ultra"}}
			r.price()
			if !knownModel(r.Model) || r.Details.PriceVersion != tc.version {
				t.Fatalf("model %s has pricing version %q, want %q for a known model", r.Model, r.Details.PriceVersion, tc.version)
			}
			rates := [5]*float64{r.InputPrice, r.Details.Prices[1], r.Details.CacheWritePrices[0], r.Details.CacheWritePrices[1], r.OutputPrice}
			for i, rate := range rates {
				if rate == nil {
					t.Fatalf("missing rate %d", i)
				}
				if !near(rate, tc.rates[i]) {
					t.Errorf("rate %d = %g, want %g", i, *rate, tc.rates[i])
				}
			}
		})
	}
	unsupported := Request{Model: "gpt-6.1-terra-ultrafast", CLI: "codex", At: at, Input: 1000, Output: 1000, Multiplier: .16}
	unsupported.price()
	if knownModel(unsupported.Model) || unsupported.InputPrice != nil || unsupported.OutputPrice != nil || unsupported.Cost != 0 {
		t.Fatalf("unsupported Ultrafast alias was priced: %+v", unsupported)
	}
}

func TestUltrafastPromotionEndsAtPacificMidnightAndPricesLongContext(t *testing.T) {
	cutoff := time.Date(2027, time.March, 1, 0, 0, 0, 0, time.FixedZone("PST", -8*3600))
	short, long := int64(20000), int64(80000)
	for _, tc := range []struct {
		name     string
		at       time.Time
		official [4]int64
		billed   [4]int64
		cost     int64
	}{
		{"promotion", cutoff.Add(-time.Millisecond), [4]int64{3456048, 240000, 10800000, 1800000}, [4]int64{552968, 38400, 1728000, 288000}, 2607368},
		{"list price", cutoff, [4]int64{6912096, 480000, 21600000, 3600000}, [4]int64{1105935, 76800, 3456000, 576000}, 5214735},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// One token over the threshold puts the whole request on long-context rates.
			r := Request{Model: "gpt-6.2-astra-ultrafast", CLI: "codex", At: tc.at.UnixMilli(), Input: 272001, Read: 100000, Write: 100000, Output: 10000, Multiplier: .16}
			r.Details.WriteShort, r.Details.WriteLong = &short, &long
			r.price()
			if r.Costs != tc.official || r.Details.Billed != tc.billed || r.Cost != tc.cost {
				t.Fatalf("official %v, billed %v, total %d; want %v, %v, %d", r.Costs, r.Details.Billed, r.Cost, tc.official, tc.billed, tc.cost)
			}
		})
	}
}

func TestSyntheticCostsNothingAndAnUnknownModelCostsZeroWithoutUnitPrices(t *testing.T) {
	for _, model := range []string{"<synthetic>", "some-future-model"} {
		r := Request{Model: model, CLI: "claude", Input: 1000, Output: 1000, Multiplier: 2}
		r.price()
		if r.Cost != 0 || r.Costs != [4]int64{} || r.Details.Billed != [4]int64{} || r.InputPrice != nil || r.OutputPrice != nil {
			t.Fatalf("%s: %+v", model, r)
		}
	}
}

func TestEveryKnownModelIsPricedAtItsOwnMultiplier(t *testing.T) {
	for model := range tariffs {
		r := Request{Model: model, Input: 1000000, Output: 1000000, Multiplier: .5}
		r.price()
		if r.Cost <= 0 || r.InputPrice == nil || r.Details.Billed[3] != int64(float64(r.Costs[3])*.5) {
			t.Fatalf("%s: %+v", model, r)
		}
	}
}

func TestDeepSeekWeekdayPeakRuleAppliesToLeaveDaysAndOtherYears(t *testing.T) {
	cst := time.FixedZone("CST", 8*3600)
	for _, c := range []struct {
		name string
		at   time.Time
		peak bool
	}{
		{"ordinary weekday peak", time.Date(2026, 10, 28, 10, 0, 0, 0, cst), true},
		{"weekday off-peak hour", time.Date(2026, 10, 28, 12, 0, 0, 0, cst), false},
		{"weekend", time.Date(2026, 10, 24, 10, 0, 0, 0, cst), false},
		{"statutory holiday", time.Date(2026, 10, 1, 10, 0, 0, 0, cst), false},
		{"holiday leave weekday", time.Date(2026, 10, 5, 10, 0, 0, 0, cst), true},
		{"make-up Saturday follows the weekend rule", time.Date(2026, 2, 28, 10, 0, 0, 0, cst), false},
		{"another year", time.Date(2027, 3, 3, 10, 0, 0, 0, cst), true},
	} {
		r := Request{Model: "deepseek-flash", At: c.at.UnixMilli(), Input: 1000000, Multiplier: 1}
		r.price()
		want := int64(150000)
		if c.peak {
			want = 300000
		}
		if r.Cost != want {
			t.Fatalf("%s: cost %d, want %d", c.name, r.Cost, want)
		}
	}
}
