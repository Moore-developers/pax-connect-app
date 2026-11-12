package aiusage

import (
	"context"
	"testing"
	"time"
)

func TestLimitsSummaryUsesActualSafePricedSamplesAndExcludesUnsetModels(t *testing.T) {
	fact := func(model string, input, money, priced int64) limitFact {
		return limitFact{root: SessionKey{"codex", "root"}, lines: modelLimit(model), row: AnalysisRow{CLI: "codex", Session: "root", Model: model, PricedRequests: priced, Summary: Summary{Input: input, Costs: [4]int64{money}}}}
	}
	facts := []limitFact{
		fact("gpt-6.1-sol", 100000, 20000, 1),
		fact("gpt-6.1-sol", 100000, 0, 0),
		fact("gpt-6.1-sol", 260000, 90000, 1),
		fact("gpt-6.1-sol", 300000, 100000, 1),
		fact("gpt-6.1-sol", 400000, 300000, 1),
		fact("gpt-6.1-sol", 400000, 0, 0),
		fact("other-unconfigured", 100000, 900000, 1),
	}
	summary := assembleLimits(facts, agentGraph{}, nil, nil).Summary
	if summary.Multiple == nil || summary.Bands[0].Requests != 2 || summary.Bands[0].PricedRequests != 1 || summary.Bands[2].PricedRequests != 2 {
		t.Fatalf("priced sample counts %+v", summary)
	}
	closeLimit(t, *summary.Multiple, 10)
	closeLimit(t, summary.Input.Official, 1.41)
	closeLimit(t, summary.OverSpend.Official, .36)
	withoutSafe := assembleLimits(facts[2:6], agentGraph{}, nil, nil)
	if withoutSafe.Summary.Multiple != nil || withoutSafe.Primary[0].Multiple == nil {
		t.Fatal("summary must not present a reference baseline as an observed safe mean")
	}
	closeLimit(t, *withoutSafe.Primary[0].Multiple, .2/.023)
}

func TestLimitsDailyTrendDeduplicatesDescendantsAndCountsRootOnEachDay(t *testing.T) {
	s, from, to := agentFixture(t)
	ctx := context.Background()
	price := 1.0
	requests := []Request{
		{CLI: "codex", Session: "root", Key: "priced-root", Input: 1000, Read: 600, Write: 200, Output: 9999, Model: "gpt-6.1-sol", At: from + 2000, Costs: [4]int64{100000, 200000, 300000, 999999}, Details: Details{Prices: [4]*float64{&price}}},
		{CLI: "codex", Session: "child", Key: "priced-child", Input: 500, Read: 100, Model: "gpt-6.1-sol", At: from + 3000, Costs: [4]int64{400000}, Details: Details{Prices: [4]*float64{&price}}},
		{CLI: "codex", Session: "child", Key: "next-day", Input: 2000, Read: 1500, Model: "gpt-6.1-sol", At: to + 1000, Costs: [4]int64{500000}, Details: Details{Prices: [4]*float64{&price}}},
	}
	if err := s.commit(ctx, requests, cursor{Path: "limits-days"}); err != nil {
		t.Fatal(err)
	}
	end := time.UnixMilli(to).AddDate(0, 0, 1).UnixMilli()
	all, err := s.AnalyzeLimits(ctx, from, end, nil)
	if err != nil {
		t.Fatal(err)
	}
	days := all.Limits.Summary.Days
	if len(days) != 2 || days[0].Sessions != 3 || days[1].Sessions != 1 || all.Limits.Summary.Sessions != 3 || !days[0].Available || !days[1].Available {
		t.Fatalf("daily roots must deduplicate child activity %+v", all.Limits.Summary)
	}
	if days[0].InputTokens != 2700 || days[1].InputTokens != 2000 || all.Limits.Summary.InputTokens != 4700 {
		t.Fatalf("total input includes caches once and unpriced descendants, never output: %+v", all.Limits.Summary)
	}
	closeLimit(t, days[0].Input.Official, 1)
	closeLimit(t, days[1].Input.Official, .5)
	selected, err := s.AnalyzeLimits(ctx, from, end, []SessionKey{{"codex", "root"}})
	if err != nil || selected.Limits.Summary.Days[0].Sessions != 1 || selected.Limits.Summary.Days[1].Sessions != 1 {
		t.Fatalf("selected root daily scope %+v %v", selected.Limits, err)
	}
	if selected.Limits.Summary.InputTokens != 4100 || selected.Limits.Summary.Days[0].InputTokens != 2100 {
		t.Fatalf("selected descendants must count once: %+v", selected.Limits.Summary)
	}
	closeLimit(t, selected.Limits.Summary.Input.Official, 1.5)
}

func TestLimitsPartialPeriodComparisonAndFutureDays(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 11, 9, 0, 0, 0, 0, time.Local)
	now := start.AddDate(0, 0, 1).Add(12 * time.Hour)
	s := openAt(t, home(t), &now, nil)
	defer func() { _ = s.Close(ctx) }()
	price := 1.0
	request := func(key string, at time.Time, amount int64) Request {
		return Request{CLI: "codex", Session: key, Key: key, Model: "gpt-6.1-sol", At: at.UnixMilli(), Input: amount, Read: amount / 2, Output: amount * 10, Costs: [4]int64{amount}, Details: Details{Prices: [4]*float64{&price}}}
	}
	end := start.AddDate(0, 0, 7)
	requests := []Request{
		request("current", start.Add(time.Hour), 200000),
		request("today", now.Add(-time.Hour), 300000),
		request("previous", start.AddDate(0, 0, -7).Add(time.Hour), 700000),
		request("after-matching-cutoff", now.AddDate(0, 0, -7).Add(time.Hour), 900000),
		request("future", now.Add(time.Hour), 800000),
	}
	if err := s.commit(ctx, requests, cursor{Path: "limits-comparison"}); err != nil {
		t.Fatal(err)
	}
	a, err := s.AnalyzeLimits(ctx, start.UnixMilli(), end.UnixMilli(), nil)
	if err != nil {
		t.Fatal(err)
	}
	summary := a.Limits.Summary
	if len(summary.Days) != 7 || summary.ObservedThrough != now.UnixMilli()+1 || !summary.Days[1].Available || summary.Days[2].Available || summary.Days[2].Requests != 0 || summary.Sessions != 2 {
		t.Fatalf("partial week %+v", summary)
	}
	closeLimit(t, summary.Input.Official, .5)
	if summary.Comparison == nil || summary.Comparison.From != start.AddDate(0, 0, -7).UnixMilli() || summary.Comparison.To != now.AddDate(0, 0, -7).UnixMilli()+1 || summary.Comparison.Sessions != 1 {
		t.Fatalf("comparison must align elapsed calendar period %+v", summary.Comparison)
	}
	if summary.InputTokens != 500000 || summary.Comparison.InputTokens != 700000 {
		t.Fatalf("input token comparison must use matching cutoff: %+v / %+v", summary, summary.Comparison)
	}
	closeLimit(t, summary.Comparison.Input.Official, .7)
	missing, err := s.AnalyzeLimits(ctx, start.UnixMilli(), end.UnixMilli(), []SessionKey{{"codex", "current"}})
	if err != nil || missing.Limits.Summary.Comparison != nil {
		t.Fatal("no prior selected-session data must not look like a zero-cost comparison", err)
	}
	// A partly priced subtotal cannot support a total-cost rise/drop claim.
	for _, period := range []struct {
		name string
		at   time.Time
	}{{"prior-unpriced", start.AddDate(0, 0, -7).Add(2 * time.Hour)}, {"current-unpriced", start.Add(2 * time.Hour)}} {
		unpriced := request(period.name, period.at, 0)
		unpriced.Details.Prices = [4]*float64{}
		if err := s.commit(ctx, []Request{unpriced}, cursor{Path: period.name}); err != nil {
			t.Fatal(err)
		}
		incomplete, err := s.AnalyzeLimits(ctx, start.UnixMilli(), end.UnixMilli(), nil)
		if err != nil || incomplete.Limits.Summary.Comparison != nil {
			t.Fatalf("%s must suppress cost comparison: %+v %v", period.name, incomplete.Limits, err)
		}
		// Restore only the test fixture's pricing metadata, isolating the next period.
		if _, err := s.w.ExecContext(ctx, "UPDATE requests SET details=json_set(details,'$.Prices[0]',1) WHERE request_key=?", period.name); err != nil {
			t.Fatal(err)
		}
		restored, err := s.AnalyzeLimits(ctx, start.UnixMilli(), end.UnixMilli(), nil)
		if err != nil || restored.Limits.Summary.Comparison == nil {
			t.Fatal("fully priced fixture should restore the comparison", err)
		}
	}
}

func TestLimitsDaysRespectDSTAndUnavailableHistory(t *testing.T) {
	location, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 31, 0, 0, 0, 0, location)
	end := start.AddDate(0, 0, 3)
	days := limitDays(nil, agentGraph{}, nil, start.UnixMilli(), end.UnixMilli(), end.UnixMilli(), start.UnixMilli(), false, location)
	if len(days) != 3 || days[2].At-days[1].At != int64(25*time.Hour/time.Millisecond) || !days[0].Available {
		t.Fatalf("DST must preserve local dates %+v", days)
	}
	from := time.Date(2026, 11, 2, 0, 0, 0, 0, location)
	observed := from.AddDate(0, 0, 1).Add(12 * time.Hour)
	previousFrom, previousTo := limitComparisonRange(from.UnixMilli(), from.AddDate(0, 0, 7).UnixMilli(), observed.UnixMilli(), location)
	if previousFrom != from.AddDate(0, 0, -7).UnixMilli() || previousTo != observed.AddDate(0, 0, -7).UnixMilli() {
		t.Fatal("comparison shifted by hours instead of calendar days")
	}
	// A week-to-date query ends after only three days, but compares against the
	// same weekdays in the complete preceding calendar week, including DST.
	week := time.Date(2026, 11, 8, 0, 0, 0, 0, location)
	through := week.AddDate(0, 0, 2).Add(12 * time.Hour)
	prior := LimitComparisonRange{week.AddDate(0, 0, -7).UnixMilli(), week.UnixMilli()}
	previousFrom, previousTo = limitComparisonRange(week.UnixMilli(), week.AddDate(0, 0, 3).UnixMilli(), through.UnixMilli(), location, prior)
	wantCutoff := time.Date(2026, 11, 3, 12, 0, 0, 0, location)
	if previousFrom != prior.From || previousTo != wantCutoff.UnixMilli() {
		t.Fatal("week-to-date must map to the same prior weekdays and wall-clock time")
	}
	expired := limitDays(nil, agentGraph{}, nil, start.UnixMilli(), end.UnixMilli(), end.UnixMilli(), start.Add(time.Hour).UnixMilli(), false, location)
	truncated := limitDays(nil, agentGraph{}, nil, start.UnixMilli(), end.UnixMilli(), end.UnixMilli(), start.UnixMilli(), true, location)
	if expired[0].Available || !expired[1].Available || truncated[0].Available {
		t.Fatal("expired or truncated records must not become known empty days")
	}
}

func TestLimitsWeekToDateUsesExplicitPriorCalendarPeriod(t *testing.T) {
	ctx := context.Background()
	week := time.Date(2026, 11, 8, 0, 0, 0, 0, time.Local)
	now := week.AddDate(0, 0, 2).Add(12 * time.Hour)
	s := openAt(t, home(t), &now, nil)
	defer func() { _ = s.Close(ctx) }()
	price := 1.0
	request := func(key string, at time.Time, money int64) Request {
		return Request{CLI: "codex", Session: key, Key: key, Model: "gpt-6.1-sol", At: at.UnixMilli(), Input: money, Read: money / 2, Output: money * 10, Costs: [4]int64{money}, Details: Details{Prices: [4]*float64{&price}}}
	}
	requests := []Request{
		request("current", week.Add(time.Hour), 200000),
		request("same-weekdays", week.AddDate(0, 0, -7).Add(time.Hour), 400000),
		request("wrong-three-days", week.AddDate(0, 0, -2).Add(time.Hour), 900000),
		request("after-same-time", now.AddDate(0, 0, -7).Add(time.Hour), 800000),
	}
	if err := s.commit(ctx, requests, cursor{Path: "calendar-comparison"}); err != nil {
		t.Fatal(err)
	}
	prior := LimitComparisonRange{From: week.AddDate(0, 0, -7).UnixMilli(), To: week.UnixMilli()}
	a, err := s.AnalyzeLimits(ctx, week.UnixMilli(), week.AddDate(0, 0, 3).UnixMilli(), nil, prior)
	if err != nil {
		t.Fatal(err)
	}
	comparison := a.Limits.Summary.Comparison
	if comparison == nil || comparison.From != prior.From || comparison.To != now.AddDate(0, 0, -7).UnixMilli()+1 || comparison.Sessions != 1 {
		t.Fatalf("wrong calendar comparison %+v", comparison)
	}
	closeLimit(t, comparison.Input.Official, .4)
	for _, invalid := range []LimitComparisonRange{
		{From: 0, To: prior.To},
		{From: prior.To, To: prior.From},
		{From: prior.From, To: prior.To + 1},
		{From: prior.To - MaxQuerySpanMillis - 1, To: prior.To},
	} {
		if _, err := s.AnalyzeLimits(ctx, week.UnixMilli(), week.AddDate(0, 0, 3).UnixMilli(), nil, invalid); err != ErrRange {
			t.Fatalf("invalid comparison accepted %+v: %v", invalid, err)
		}
	}
}

func TestLimitsMonthComparisonRequiresEqualElapsedCalendarSpan(t *testing.T) {
	ctx := context.Background()
	month := time.Date(2026, 3, 1, 0, 0, 0, 0, time.Local)
	now := month.AddDate(0, 0, 30).Add(12 * time.Hour)
	s := openAt(t, home(t), &now, nil)
	defer func() { _ = s.Close(ctx) }()
	prior := LimitComparisonRange{From: month.AddDate(0, -1, 0).UnixMilli(), To: month.UnixMilli()}
	price := 1.0
	var requests []Request
	for _, entry := range []struct {
		key string
		at  int64
	}{{"prior", prior.From + 1000}, {"current", month.UnixMilli() + 1000}} {
		requests = append(requests, Request{CLI: "codex", Key: entry.key, Session: entry.key, At: entry.at, Model: "gpt-6.1-sol", Costs: [4]int64{100000}, Details: Details{Prices: [4]*float64{&price}}})
	}
	if err := s.commit(ctx, requests, cursor{Path: "month-span"}); err != nil {
		t.Fatal(err)
	}
	query := func(to time.Time) *LimitComparison {
		t.Helper()
		a, err := s.AnalyzeLimits(ctx, month.UnixMilli(), to.UnixMilli(), nil, prior)
		if err != nil {
			t.Fatal(err)
		}
		return a.Limits.Summary.Comparison
	}
	if comparison := query(month.AddDate(0, 1, 0)); comparison != nil {
		t.Fatal("31 March days must not compare with 28 February days", comparison)
	}
	ended := month.AddDate(0, 0, 28)
	if comparison := query(ended); comparison == nil || comparison.To != prior.To {
		t.Fatal("equal completed periods should compare without adding a millisecond", comparison)
	}
	now = month.AddDate(0, 0, 27).Add(12 * time.Hour)
	if comparison := query(month.AddDate(0, 1, 0)); comparison == nil || comparison.To != time.Date(2026, 2, 28, 12, 0, 0, 0, time.Local).UnixMilli()+1 {
		t.Fatal("partial month must retain the matching prior wall-clock cutoff", comparison)
	}
}
