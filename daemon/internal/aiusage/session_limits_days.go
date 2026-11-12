package aiusage

import (
	"context"
	"database/sql"
	"time"
)

func limitDayStart(at int64, location *time.Location) time.Time {
	t := time.UnixMilli(at).In(location)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, location)
}

func limitDays(facts []limitFact, graph agentGraph, selected *SessionKey, from, to, observed, retained int64, partial bool, location *time.Location) []LimitDay {
	var days []LimitDay
	indexes := map[int64]int{}
	for day := limitDayStart(from, location); day.UnixMilli() < to; day = day.AddDate(0, 0, 1) {
		at := day.UnixMilli()
		indexes[at] = len(days)
		days = append(days, LimitDay{At: at, Available: !partial && max(at, from) >= retained && max(at, from) < observed})
	}
	sessions := make([]map[SessionKey]bool, len(days))
	for _, fact := range facts {
		if fact.row.At < from || fact.row.At >= observed || !limitFactInScope(fact, graph, selected) {
			continue
		}
		index, found := indexes[limitDayStart(fact.row.At, location).UnixMilli()]
		if !found || !days[index].Available {
			continue
		}
		day := &days[index]
		day.Requests++
		day.InputTokens += fact.row.Input
		if sessions[index] == nil {
			sessions[index] = map[SessionKey]bool{}
		}
		sessions[index][fact.root] = true
		day.Sessions = int64(len(sessions[index]))
		if fact.row.PricedRequests > 0 {
			day.PricedRequests++
			day.Input.add(fact.money())
		}
	}
	return days
}

// Calendar shifts preserve local wall-clock time across 23/25-hour days.
func limitComparisonRange(from, to, observed int64, location *time.Location, comparison ...LimitComparisonRange) (int64, int64) {
	if len(comparison) == 1 {
		prior := comparison[0]
		start := time.UnixMilli(from).In(location)
		cutoff := time.UnixMilli(observed).In(location)
		previous := time.UnixMilli(prior.From).In(location)
		// A UTC wall-clock duration expresses calendar-day and time-of-day offsets
		// without turning the DST transition into a one-hour shift.
		wall := func(t time.Time) time.Time {
			return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
		}
		target := wall(previous).Add(wall(cutoff).Sub(wall(start)))
		end := time.Date(target.Year(), target.Month(), target.Day(), target.Hour(), target.Minute(), target.Second(), target.Nanosecond(), location).UnixMilli()
		if end > prior.To {
			// A longer elapsed current period has no equal prior calendar span.
			return prior.From, prior.From
		}
		return prior.From, end
	}
	start, end := time.UnixMilli(from).In(location), time.UnixMilli(to).In(location)
	startDate := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	endDate := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.UTC)
	days := int(endDate.Sub(startDate) / (24 * time.Hour))
	clock := func(t time.Time) time.Duration {
		return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute + time.Duration(t.Second())*time.Second + time.Duration(t.Nanosecond())
	}
	if clock(end) > clock(start) {
		days++
	}
	days = max(1, days)
	return start.AddDate(0, 0, -days).UnixMilli(), time.UnixMilli(observed).In(location).AddDate(0, 0, -days).UnixMilli()
}

func readLimitComparison(ctx context.Context, tx *sql.Tx, graph agentGraph, lines map[string]*ModelLimit, selected *SessionKey, from, to, observed, retained int64, current []LimitDay, partial bool, location *time.Location, comparison ...LimitComparisonRange) (*LimitComparison, error) {
	if partial || from < retained || observed <= from {
		return nil, nil
	}
	var priced int64
	for _, day := range current {
		if day.PricedRequests < day.Requests {
			return nil, nil
		}
		priced += day.PricedRequests
	}
	if priced == 0 {
		return nil, nil
	}
	previousFrom, previousTo := limitComparisonRange(from, to, observed, location, comparison...)
	if previousFrom < retained || previousTo <= previousFrom {
		return nil, nil
	}
	facts, truncated, err := readLimitFacts(ctx, tx, graph, lines, "r.at>=? AND r.at<?", []any{previousFrom, previousTo}, 100000)
	if err != nil || truncated {
		return nil, err
	}
	result := &LimitComparison{From: previousFrom, To: previousTo}
	sessions := map[SessionKey]bool{}
	priced = 0
	for _, fact := range facts {
		if !limitFactInScope(fact, graph, selected) {
			continue
		}
		if fact.row.PricedRequests == 0 {
			return nil, nil
		}
		sessions[fact.root] = true
		result.InputTokens += fact.row.Input
		if fact.row.PricedRequests > 0 {
			priced++
			result.Input.add(fact.money())
		}
	}
	if priced == 0 {
		return nil, nil
	}
	result.Sessions = int64(len(sessions))
	return result, nil
}
