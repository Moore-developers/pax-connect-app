package aiusage

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestLimitsDeepSeekUsesEconomicBandsWithoutRepricing(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	s := openAt(t, home(t), &now, nil)
	defer func() { _ = s.Close(ctx) }()
	from, to := now.Add(-time.Hour).UnixMilli(), now.Add(time.Hour).UnixMilli()
	request := func(key, session string, at, input int64) Request {
		r := Request{CLI: "dsh", Key: key, Session: session, Model: "deepseek-flash", At: at, Input: input, Read: input - 1000, Output: 1000000, Multiplier: 1, Succeeded: true}
		r.price()
		return r
	}
	// The old mixed-period reference cannot fill a missing same-period baseline.
	if err := s.commit(ctx, []Request{request("over-only", "over", from+100, 300001)}, cursor{Path: "deepseek-sparse"}); err != nil {
		t.Fatal(err)
	}
	sparse, err := s.AnalyzeLimits(ctx, from, to, []SessionKey{{"dsh", "over"}})
	if err != nil {
		t.Fatal(err)
	}
	if sparse.Limits.Primary[0].Lines.Baseline != nil || sparse.Limits.Primary[0].Multiple != nil || sparse.Limits.Primary[0].OverSpend != nil {
		t.Fatal("missing same-period baseline must stay unknown")
	}
	var requests []Request
	for i := 0; i < 50; i++ {
		requests = append(requests, request(fmt.Sprint(i), "baseline", from-1000, 100000))
	}
	for i, input := range []int64{250000, 250001, 300000, 300001} {
		requests = append(requests, request(fmt.Sprintf("boundary-%d", i), "selected", from+int64(i), input))
	}
	if err := s.commit(ctx, requests, cursor{Path: "deepseek-limits"}); err != nil {
		t.Fatal(err)
	}
	a, err := s.AnalyzeLimits(ctx, from, to, []SessionKey{{"dsh", "selected"}})
	if err != nil {
		t.Fatal(err)
	}
	row := a.Limits.Primary[0]
	if row.Lines == nil || row.Lines.Warn != 250000 || row.Lines.Limit != 300000 || row.Flat != nil {
		t.Fatalf("economic preset %+v", row)
	}
	// Boundaries are inclusive: 250k safe, 300k warning, then over.
	if row.Bands[0].Requests != 1 || row.Bands[1].Requests != 2 || row.Bands[2].Requests != 1 {
		t.Fatalf("bands %+v", row.Bands)
	}
	// The retained 50-request measured baseline replaces the reference preset.
	closeLimit(t, *row.Lines.Baseline, .000447)
	closeLimit(t, a.Limits.Summary.Input.Official, .003888)
	closeLimit(t, *row.Multiple, .001047/.000897)
	closeLimit(t, row.OverSpend.Official, .00015)
	closeLimit(t, row.OverSpend.Channel, .00015)
	if a.Limits.Summary.Current.Limit == nil || *a.Limits.Summary.Current.Limit != 300000 || a.Limits.Summary.Current.Band != 2 {
		t.Fatalf("current %+v", a.Limits.Summary.Current)
	}
	trend := a.Limits.Trend
	if trend.Lines == nil || trend.Lines.Warn != 250000 || trend.OverStart == nil || *trend.OverStart != 3 || trend.FlatStart != nil {
		t.Fatalf("trend %+v", trend)
	}
	for i, point := range trend.Requests {
		want := []int32{0, 1, 1, 2}[i]
		if point.Band != want {
			t.Fatalf("request %d band %d want %d", i, point.Band, want)
		}
		if point.Request.Costs[0] != 150 || point.Request.Costs[1] != []int64{747, 747, 897, 897}[i] {
			t.Fatalf("request %d repriced across economic threshold: %+v", i, point.Request.Costs)
		}
	}
	// A selected session without safe requests uses the retained model baseline.
	a, err = s.AnalyzeLimits(ctx, from, to, []SessionKey{{"dsh", "over"}})
	if err != nil {
		t.Fatal(err)
	}
	closeLimit(t, *a.Limits.Primary[0].Multiple, .001047/.000447)
	closeLimit(t, a.Limits.Primary[0].OverSpend.Official, .0006)
}

func TestLimitsDeepSeekFamilyDefaultsKeepModelPricesIndependent(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	s := openAt(t, home(t), &now, nil)
	defer func() { _ = s.Close(ctx) }()
	peak := now.Add(-5 * time.Hour).UnixMilli()
	off := now.Add(-time.Hour).UnixMilli()
	var requests []Request
	for i, at := range []int64{peak, off} {
		for j, input := range []int64{100000, 300001} {
			r := Request{CLI: "dsh", Key: fmt.Sprintf("tariff-%d-%d", i, j), Session: "alias", Model: "deepseek-v4.1-flash", At: at + int64(j), Input: input, Read: input - 1000, Multiplier: 1, Succeeded: true}
			r.price()
			requests = append(requests, r)
		}
	}
	for _, model := range []string{"deepseek-v4-pro", "deepseek-future", "deepseek"} {
		r := Request{CLI: "dsh", Key: model, Session: model, Model: model, At: off, Input: 300001, Multiplier: 1, Succeeded: true}
		r.price()
		requests = append(requests, r)
	}
	if err := s.commit(ctx, requests, cursor{Path: "deepseek-family"}); err != nil {
		t.Fatal(err)
	}
	a, err := s.AnalyzeLimits(ctx, peak-1, now.UnixMilli(), []SessionKey{{"dsh", "alias"}})
	if err != nil {
		t.Fatal(err)
	}
	row := a.Limits.Primary[0]
	if row.Lines == nil || row.Lines.Warn != 250000 || row.Lines.Limit != 300000 || row.Lines.Baseline != nil || row.Bands[0].Requests != 2 || row.Bands[2].Requests != 2 {
		t.Fatalf("alias defaults %+v", row)
	}
	closeLimit(t, *row.Multiple, .0015705/.0006705)
	points := a.Limits.Trend.Requests
	for i, point := range points {
		if point.Band != []int32{0, 2, 0, 2}[i] {
			t.Fatalf("tariff changed band: %+v", point)
		}
	}
	// The same token mix is priced 2x at peak, independent of the shared limit.
	for component := 0; component < 2; component++ {
		if points[0].Request.Costs[component] != 2*points[2].Request.Costs[component] || points[1].Request.Costs[component] != 2*points[3].Request.Costs[component] {
			t.Fatal("peak/off-peak ledger amounts were mixed")
		}
	}
	for _, model := range []string{"deepseek-v4-pro", "deepseek-future", "deepseek"} {
		a, err = s.AnalyzeLimits(ctx, peak-1, now.UnixMilli(), []SessionKey{{"dsh", model}})
		if err != nil {
			t.Fatal(err)
		}
		row = a.Limits.Primary[0]
		if row.Lines == nil || row.Lines.Warn != 250000 || row.Lines.Limit != 300000 || row.Lines.Baseline != nil || row.Bands[2].Requests != 1 || row.Unpriced != 1 || row.Multiple != nil || row.OverSpend != nil {
			t.Fatalf("unpriced family model %s: %+v", model, row)
		}
	}
}

func TestLimitsDeepSeekPeriodMatchingAndRetainedFallback(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	s := openAt(t, home(t), &now, nil)
	defer func() { _ = s.Close(ctx) }()
	peak := now.Add(-5 * time.Hour).UnixMilli()
	off := now.Add(-time.Hour).UnixMilli()
	price := .15
	fact := func(model string, at, input, micro int64) limitFact {
		return limitFact{lines: modelLimit(model), row: AnalysisRow{CLI: "dsh", Session: "s", Model: model, At: at, PricedRequests: 1, Summary: Summary{Input: input, Costs: [4]int64{micro}, BilledCosts: [4]int64{micro / 2}}}}
	}
	// A peak request costing twice a valley request is not extra context spend.
	fs := []limitFact{fact("deepseek-flash", peak, 100000, 2000), fact("deepseek-flash", off, 100000, 1000), fact("deepseek-flash", peak, 300001, 2000)}
	row := limitRow("s", "dsh", "s", "s", fs)
	closeLimit(t, *row.Multiple, 1)
	closeLimit(t, row.OverSpend.Official, 0)
	summary := assembleLimits(fs, agentGraph{}, nil, nil).Summary
	closeLimit(t, *summary.Multiple, 1)
	// Weight matched safe means by the over-limit mix, not the safe sample mix.
	fs = append(fs, fact("deepseek-flash", peak, 300002, 4000), fact("deepseek-flash", off, 300001, 1000))
	row = limitRow("s", "dsh", "s", "s", fs)
	closeLimit(t, *row.Multiple, .007/.005)
	closeLimit(t, row.OverSpend.Official, .002)
	closeLimit(t, row.OverSpend.Channel, .001)
	closeLimit(t, *assembleLimits(fs, agentGraph{}, nil, nil).Summary.Multiple, .007/.005)
	// Other model/period safe requests cannot substitute for a missing matching group.
	fs = []limitFact{fact("deepseek-flash", off, 100000, 1000), fact("deepseek-v4.1-flash", peak, 100000, 2000), fact("deepseek-flash", peak, 300001, 2000)}
	row = limitRow("s", "dsh", "s", "s", fs)
	if row.Multiple != nil || row.OverSpend != nil || assembleLimits(fs, agentGraph{}, nil, nil).Summary.Multiple != nil {
		t.Fatal("cross-period/model baseline leaked")
	}
	var requests []Request
	for i := 0; i < 50; i++ {
		requests = append(requests, Request{CLI: "dsh", Key: fmt.Sprintf("off-%d", i), Model: "deepseek-flash", At: off - 86400000, Input: 100000, Costs: [4]int64{1000}, Details: Details{Prices: [4]*float64{&price}}})
		if i < 49 {
			requests = append(requests, Request{CLI: "dsh", Key: fmt.Sprintf("peak-%d", i), Model: "deepseek-flash", At: peak - 86400000, Input: 100000, Costs: [4]int64{2000}, Details: Details{Prices: [4]*float64{&price}}})
		}
	}
	requests = append(requests, Request{CLI: "dsh", Key: "selected", Session: "selected", Model: "deepseek-flash", At: peak, Input: 300001, Costs: [4]int64{4000}, Details: Details{Prices: [4]*float64{&price}, Billed: [4]int64{2000}}})
	if err := s.commit(ctx, requests, cursor{Path: "period-baseline"}); err != nil {
		t.Fatal(err)
	}
	query := func() Analysis {
		a, e := s.AnalyzeLimits(ctx, peak-1, now.UnixMilli(), []SessionKey{{"dsh", "selected"}})
		if e != nil {
			t.Fatal(e)
		}
		return a
	}
	a := query()
	if a.Limits.Primary[0].Multiple != nil {
		t.Fatal("49 peak + 50 valley must not become 99 same-period samples")
	}
	r := Request{CLI: "dsh", Key: "peak-50", Model: "deepseek-flash", At: peak - 86400000, Input: 100000, Costs: [4]int64{2000}, Details: Details{Prices: [4]*float64{&price}}}
	if err := s.commit(ctx, []Request{r}, cursor{Path: "period-baseline-more"}); err != nil {
		t.Fatal(err)
	}
	a = query()
	row = a.Limits.Primary[0]
	closeLimit(t, *row.Lines.Baseline, .002)
	closeLimit(t, *row.Multiple, 2)
	closeLimit(t, row.OverSpend.Official, .002)
	closeLimit(t, row.OverSpend.Channel, .001)
	if a.Limits.Summary.Multiple != nil {
		t.Fatal("summary must not present history as observed safe")
	}
}
