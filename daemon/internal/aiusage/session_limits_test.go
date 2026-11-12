package aiusage

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func closeLimit(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("amount got %.12f want %.12f", got, want)
	}
}
func TestLimitsJudgeModelBandsAndInputOnlyAmounts(t *testing.T) {
	fact := func(model string, input int64, cost, channel float64, priced bool) limitFact {
		r := AnalysisRow{Model: model, Summary: Summary{Input: input, Costs: [4]int64{int64(cost * 1e6), 0, 0, 999000000}, BilledCosts: [4]int64{int64(channel * 1e6), 0, 0, 555000000}}}
		if priced {
			r.PricedRequests = 1
		}
		return limitFact{row: r, lines: modelLimit(model)}
	}
	facts := []limitFact{fact("gpt-6.1-sol", 250000, .02, .002, true), fact("gpt-6.1-sol", 272000, .04, .004, true), fact("gpt-6.1-sol", 300000, .12, .012, true), fact("gpt-6.1-sol", 400000, .24, .024, true), fact("gpt-6.1-sol", 500000, 0, 0, false)}
	row := limitRow("r", "codex", "s", "test", facts)
	if row.Bands[0].Requests != 1 || row.Bands[1].Requests != 1 || row.Bands[2].Requests != 3 || row.Bands[2].PricedRequests != 2 || row.Unpriced != 1 {
		t.Fatalf("boundaries %+v", row)
	}
	closeLimit(t, row.Bands[2].Money.Official, .36)
	closeLimit(t, *row.Multiple, 9)
	closeLimit(t, row.OverSpend.Official, .32)
	closeLimit(t, row.OverSpend.Channel, .032)
	// Flat inputs undo doubling above 272k; <=300k is the alternative safe baseline.
	closeLimit(t, row.Flat.Cost.Official, .12)
	closeLimit(t, row.Flat.OverSpend.Official, .08)
	row = limitRow("r", "codex", "s", "test", facts[3:4])
	closeLimit(t, *row.Multiple, .24/.023)
	closeLimit(t, row.OverSpend.Channel, .024-.0023)
	row = limitRow("r", "codex", "s", "test", []limitFact{fact("gpt-new", 400000, .2, .02, true)})
	if row.Lines == nil || row.Multiple != nil || row.OverSpend != nil {
		t.Fatalf("no baseline must be unknown %+v", row)
	}
	row = limitRow("r", "dsh", "s", "test", []limitFact{fact("other-unconfigured", 500000, .2, .02, true)})
	if row.Lines != nil || row.Multiple != nil || row.OverSpend != nil || row.Bands[0].Requests != 1 {
		t.Fatalf("unset limits %+v", row)
	}
}
func TestLimitsNativeAgentsAreDisjointAndMainTrendExcludesClaudeAgents(t *testing.T) {
	s, from, to := agentFixture(t)
	ctx := context.Background()
	all, err := s.AnalyzeLimits(ctx, from, to, nil)
	if err != nil {
		t.Fatal(err)
	}
	if all.Limits.Summary.Requests != 7 || len(all.Limits.Primary) != 3 || all.Limits.Summary.Subagents != 5 {
		t.Fatalf("global totals %+v", all.Limits)
	}
	for _, cli := range []string{"claude", "codex", "dsh"} {
		a, e := s.AnalyzeLimits(ctx, from, to, []SessionKey{{cli, "root"}})
		if e != nil {
			t.Fatal(e)
		}
		want := int64(2)
		agents := int64(1)
		if cli == "codex" {
			want = 3
			agents = 3
		}
		if a.Limits.Summary.Requests != want || a.Limits.Summary.Subagents != agents || len(a.Limits.Secondary) != int(agents) || len(a.Limits.Trend.Requests) != 1 || a.Limits.Primary[0].Bands[0].Requests != 1 {
			t.Fatalf("%s scopes %+v", cli, a.Limits)
		}
		if a.Limits.Trend.Requests[0].Request.RequestID != "main" {
			t.Fatal("agent leaked into main trend")
		}
	}
}
func TestLimitsDirectChildSelectionKeepsItsOwnMainLine(t *testing.T) {
	s, from, to := agentFixture(t)
	a, err := s.AnalyzeLimits(context.Background(), from, to, []SessionKey{{"codex", "child"}})
	if err != nil {
		t.Fatal(err)
	}
	if a.Limits.Summary.Requests != 2 || a.Limits.Primary[0].Bands[0].Requests != 1 || len(a.Limits.Secondary) != 1 || len(a.Limits.Trend.Requests) != 1 {
		t.Fatalf("selected child must be relative main: %+v", a.Limits)
	}
}

func TestLimitsRetainedBaselineWindowAndNativeTurnIdentity(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	s := openAt(t, home(t), &now, nil)
	defer func() { _ = s.Close(ctx) }()
	from := now.Add(-time.Hour).UnixMilli()
	to := now.Add(time.Hour).UnixMilli()
	var requests []Request
	price := 2.0
	for i := 0; i < 50; i++ {
		requests = append(requests, Request{CLI: "codex", Key: fmt.Sprint(i), Session: "baseline", Model: "gpt-6.1-sol", At: from - 1000, Input: 100000, Costs: [4]int64{10000, 10000, 10000, 900000}, Details: Details{Prices: [4]*float64{&price}}})
	}
	requests = append(requests, Request{CLI: "codex", Key: "over", Session: "selected", Model: "gpt-6.1-sol", At: from + 1, Input: 300000, Read: 200000, Costs: [4]int64{60000, 60000, 0, 900000}, Details: Details{Prices: [4]*float64{&price}, Billed: [4]int64{6000, 6000, 0, 90000}}, turn: "one"}, Request{CLI: "codex", Key: "old", Session: "selected", Model: "gpt-6.1-sol", At: from - 2000, Input: 100000, Costs: [4]int64{10000, 0, 0, 999999}, Details: Details{Prices: [4]*float64{&price}}, turn: "one"})
	if err := s.commit(ctx, requests, cursor{Path: "limits-fixture"}); err != nil {
		t.Fatal(err)
	}
	a, err := s.AnalyzeLimits(ctx, from, to, []SessionKey{{"codex", "selected"}})
	if err != nil {
		t.Fatal(err)
	}
	if a.Limits.Summary.Requests != 1 || a.Limits.Summary.Turns != 1 || !a.Limits.Summary.TurnsComplete || len(a.Limits.Trend.Requests) != 2 {
		t.Fatalf("window/retention %+v", a.Limits)
	}
	// Historical baselines include all retained <=150k requests, not the selected date scope.
	closeLimit(t, *a.Limits.Primary[0].Lines.Baseline, (50*.03+.01)/51)
	closeLimit(t, *a.Limits.Primary[0].Multiple, .12/((50*.03+.01)/51))
	closeLimit(t, a.Limits.Summary.Input.Official, .12)
	if a.Limits.Trend.Requests[0].TurnNumber == nil || *a.Limits.Trend.Requests[1].TurnNumber != 0 {
		t.Fatal("one native turn must stay one turn")
	}
	if _, e := s.AnalyzeLimits(ctx, from, to, []SessionKey{{"bad", "s"}}); e != ErrRange {
		t.Fatalf("invalid CLI %v", e)
	}
}
func TestLimitsSwitchesDoNotSkipUnpricedNearestCacheReads(t *testing.T) {
	fs := []limitFact{}
	for i, model := range []string{"gpt-6.1-sol", "gpt-6.1-sol", "gpt-6-astra", "gpt-6-astra"} {
		r := AnalysisRow{Model: model, Effort: "high", PricedRequests: 1, Summary: Summary{Input: 300000, Read: 100000, Costs: [4]int64{10000, 10000, 0, 0}}}
		if i == 1 {
			r.PricedRequests = 0
		}
		if i == 3 {
			r.Effort = "medium"
		}
		fs = append(fs, limitFact{row: r, lines: modelLimit(model)})
	}
	v := limitTrend(fs)
	if len(v.Switches) != 2 || v.Switches[0].AfterReadMultiple != nil || v.Switches[0].TurnMultiple != nil || v.Switches[1].Kind != "effort" {
		t.Fatalf("switches %+v", v.Switches)
	}
}

func TestLimitsClaudeAgentLabelChangesWithoutRecountingLedger(t *testing.T) {
	s, from, to := agentFixture(t)
	ctx := context.Background()
	before := s.State()
	path := filepath.Join(s.options.Home, ".claude", "projects", "p", "root", "subagents", "agent-worker.meta.json")
	if err := os.WriteFile(path, []byte(`{"agentType":"Explore","description":"private-description-must-not-be-indexed"}`), 0600); err != nil {
		t.Fatal(err)
	}
	scan := func() {
		t.Helper()
		files, sources := s.sources()
		if err := s.scanCatalog(ctx, files, sources); err != nil {
			t.Fatal(err)
		}
	}
	scan()
	a, err := s.AnalyzeLimits(ctx, from, to, []SessionKey{{"claude", "root"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Limits.Secondary) != 1 || a.Limits.Secondary[0].Title != "Explore" {
		t.Fatalf("native name missing %+v", a.Limits.Secondary)
	}
	if s.State().Revision != before.Revision || s.State().Generation != before.Generation || s.State().CatalogRevision <= before.CatalogRevision {
		t.Fatal("name must invalidate catalog only")
	}
	var raw string
	if err = s.r.QueryRow(`SELECT state FROM session_catalog_files WHERE path=?`, strings.TrimSuffix(path, ".meta.json")+".jsonl").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "private-description") {
		t.Fatal("description leaked into directory")
	}
	// Sidecar changes must be seen even when the request log itself did not change.
	if err = os.WriteFile(path, []byte(`{"agentType":"code-reviewer"}`), 0600); err != nil {
		t.Fatal(err)
	}
	scan()
	a, err = s.AnalyzeLimits(ctx, from, to, []SessionKey{{"claude", "root"}})
	if err != nil || a.Limits.Secondary[0].Title != "code-reviewer" {
		t.Fatalf("late metadata %+v %v", a.Limits.Secondary, err)
	}
	catalog := s.State().CatalogRevision
	scan()
	if s.State().CatalogRevision != catalog {
		t.Fatal("unchanged scan invalidated catalog")
	}
}

func TestLimitsSessionOrderUsesRecentNativeActivityInsteadOfCost(t *testing.T) {
	root := SessionKey{"codex", "root"}
	child := SessionKey{"codex", "child"}
	older := SessionKey{"codex", "expensive"}
	same := SessionKey{"claude", "same-time"}
	g := agentGraph{
		Catalog: map[SessionKey]SessionCatalogEntry{
			root: {LastActivity: 100}, child: {LastActivity: 500}, older: {LastActivity: 200}, same: {LastActivity: 500},
		},
		Sessions: map[SessionKey]nativeAgent{child: {Parent: "root"}},
	}
	fact := func(k SessionKey, at, cost int64) limitFact {
		return limitFact{root: k, lines: modelLimit("gpt-6.1-sol"), row: AnalysisRow{CLI: k.CLI, Session: k.Session, At: at, Model: "gpt-6.1-sol", PricedRequests: 1, Summary: Summary{Input: 300000, Costs: [4]int64{cost}}}}
	}
	rows := assembleLimits([]limitFact{
		fact(older, 200, 900000), fact(root, 100, 1000), fact(same, 100, 500), fact(SessionKey{"dsh", "no-catalog"}, 300, 100),
	}, g, nil, nil).Primary
	want := []SessionKey{same, root, {"dsh", "no-catalog"}, older}
	if len(rows) != len(want) {
		t.Fatalf("rows: %+v", rows)
	}
	for i, key := range want {
		if rows[i].CLI != key.CLI || rows[i].Session != key.Session {
			t.Fatalf("row %d: got %s/%s want %+v", i, rows[i].CLI, rows[i].Session, key)
		}
	}
}
