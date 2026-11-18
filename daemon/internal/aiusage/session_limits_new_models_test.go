package aiusage

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestLimitsNewCodexModelsHavePricedBandsAfterReopening(t *testing.T) {
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		t.Run(model, func(t *testing.T) {
			ctx := context.Background()
			now := local(2026, 11, 17, 12, 0)
			h := home(t)
			s := openAt(t, h, &now, nil)
			from, to := now.Add(-time.Hour).UnixMilli(), now.Add(time.Hour).UnixMilli()
			var requests []Request
			// Reproduce requests collected while the new model had no tariff.
			for i, input := range []int64{250000, 272000, 300000} {
				requests = append(requests, Request{CLI: "codex", Session: "new-model", Key: fmt.Sprint(i), Model: model, At: from + int64(i), Input: input, Read: input - 1000, Output: 1000, Multiplier: .16, Succeeded: true})
			}
			if err := s.commit(ctx, requests, cursor{Path: "before-tariff"}); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(ctx); err != nil {
				t.Fatal(err)
			}
			s = openAt(t, h, &now, nil)
			defer func() { _ = s.Close(ctx) }()
			a, err := s.AnalyzeLimits(ctx, from, to, []SessionKey{{"codex", "new-model"}})
			if err != nil {
				t.Fatal(err)
			}
			row := a.Limits.Primary[0]
			for i, band := range row.Bands {
				if band.Requests != 1 || band.PricedRequests != 1 || band.Money.Official <= 0 {
					t.Errorf("band %d has no priced share: %+v", i, band)
				}
			}
			if row.Unpriced != 0 || a.Limits.Summary.Input.Official <= 0 || row.Multiple == nil || row.OverSpend == nil {
				t.Fatalf("new model still has missing cost analysis: %+v", row)
			}
			// Historical .16 multiplier survives backfill; outputs never enter shares.
			unit := 2.0
			if model == "gpt-6.2-astra" {
				unit = 8
			}
			closeLimit(t, a.Limits.Summary.Input.Official, .0599*unit)
			closeLimit(t, a.Limits.Summary.Input.Channel, .0599*unit*.16)
			// Fresh requests use the same official prices, including both cache-write TTLs.
			r := Request{Model: model, Input: 100000, Read: 50000, Write: 10000, Output: 1000, Multiplier: .16}
			r.price()
			want := [4]int64{int64(40000 * unit), int64(2500 * unit), int64(12500 * unit), int64(5000 * unit)}
			if r.Costs != want || r.Details.CacheWritePrices[1] == nil || *r.Details.CacheWritePrices[1] != unit*2.5 {
				t.Fatalf("new request prices: got %+v want %+v", r.Costs, want)
			}
		})
	}
}
