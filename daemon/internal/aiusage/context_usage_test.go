package aiusage

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestContextUsageBandsCostsAndDeduplicatedTurns(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	s := openAt(t, home(t), &now, nil)
	t.Cleanup(func() { _ = s.Close(ctx) })
	from, to := now.Add(-time.Hour).UnixMilli(), now.UnixMilli()
	price := 1.0
	var requests []Request
	// One turn crosses size bands and models. The second session reuses the native
	// turn ID; it must still count separately in the all-session total.
	for i, input := range []int64{150000, 150001, 250000, 250001, 100, 200, 0, 10} {
		r := Request{Key: fmt.Sprint(i), CLI: "claude", Session: "s1", Model: "m1", turn: "t", At: from + int64(i), Input: input, Read: input / 2, Output: 1000000, Costs: [4]int64{10, 20, 30, 900}, Details: Details{Prices: [4]*float64{&price}, Billed: [4]int64{1, 2, 3, 90}}}
		if i == 2 {
			r.Model = "m2"
		}
		if i == 4 {
			r.Session = "s2"
		}
		if i == 5 {
			r.CLI = "codex"
		}
		if i == 6 {
			r.turn, r.Model = "", "unknown"
			r.Details.Prices[0] = nil
			r.Costs, r.Details.Billed = [4]int64{}, [4]int64{}
		}
		if i == 7 {
			r.At = to // Exclusive upper date boundary, excluded even for a session filter.
		}
		requests = append(requests, r)
	}
	if err := s.commit(ctx, requests, cursor{Path: "context-fixture"}); err != nil {
		t.Fatal(err)
	}
	a, err := s.Analyze(ctx, from, to, nil)
	if err != nil {
		t.Fatal(err)
	}
	find := func(cli, session, model string) ContextUsage {
		t.Helper()
		for _, row := range a.ContextUsage {
			if row.CLI == cli && row.Session == session && row.Model == model {
				return row
			}
		}
		t.Fatalf("missing context group %q/%q/%q", cli, session, model)
		return ContextUsage{}
	}
	all := find("", "", "")
	if all.Requests != 7 || all.KnownTurnRequests != 6 || all.Turns != 3 || a.Truncated {
		t.Fatalf("all-session deduplication: %+v", all)
	}
	want := [3]ContextBand{
		{Input: 150300, OfficialCost: 180, ChannelCost: 18, Requests: 4, PricedRequests: 3},
		{Input: 400001, OfficialCost: 120, ChannelCost: 12, Requests: 2, PricedRequests: 2},
		{Input: 250001, OfficialCost: 60, ChannelCost: 6, Requests: 1, PricedRequests: 1},
	}
	if all.Bands != want {
		t.Fatalf("boundaries, input-only amounts or historical billed amounts differ: got %+v want %+v", all.Bands, want)
	}
	single := find("claude", "s1", "")
	if single.Requests != 5 || single.KnownTurnRequests != 4 || single.Turns != 1 {
		t.Fatalf("single session must deduplicate across models/bands: %+v", single)
	}
	if find("claude", "s1", "m1").Turns != 1 || find("claude", "s1", "m2").Turns != 1 {
		t.Fatal("mixed-model contributions lost")
	}
	empty, err := s.Analyze(ctx, from-1000, from, nil)
	if err != nil || len(empty.ContextUsage) != 0 {
		t.Fatalf("empty window: %+v %v", empty, err)
	}
	full, err := s.Analyze(ctx, from, to, []SessionKey{{CLI: "claude", Session: "s1"}})
	if err != nil || len(full.ContextUsage) != 0 || len(full.Requests) != 6 {
		t.Fatalf("cache trajectory must retain full-session scope: %+v %v", full, err)
	}
}

func TestContextUsageSeparatesEffortsAndKeepsTotalTurnDeduplicated(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	s := openAt(t, home(t), &now, nil)
	defer func() { _ = s.Close(ctx) }()
	var requests []Request
	for i, effort := range []string{"low", "max", ""} {
		requests = append(requests, Request{CLI: "codex", Key: fmt.Sprint(i), Session: "s", Model: "same", At: now.UnixMilli() - 1, Input: 100, turn: "one", Details: Details{ReasoningEffort: effort}})
	}
	if err := s.commit(ctx, requests, cursor{Path: "efforts"}); err != nil {
		t.Fatal(err)
	}
	a, err := s.Analyze(ctx, now.UnixMilli()-1000, now.UnixMilli(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range []string{"", "s"} {
		groups := map[string]ContextUsage{}
		for _, r := range a.ContextUsage {
			if r.Session == session {
				groups[r.Model+"/"+r.Effort] = r
			}
		}
		if len(groups) != 4 || groups["/"].Requests != 3 || groups["/"].Turns != 1 {
			t.Fatalf("total/effort groups: %+v", groups)
		}
		for _, effort := range []string{"low", "max", ""} {
			r := groups["same/"+effort]
			if r.Requests != 1 || r.Turns != 1 || r.Bands[0].Input != 100 {
				t.Fatalf("effort %q: %+v", effort, r)
			}
		}
	}
}
