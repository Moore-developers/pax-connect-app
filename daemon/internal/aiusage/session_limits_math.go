package aiusage

import (
	"sort"
	"strings"
)

type limitFact struct {
	row               AnalysisRow
	agent, agentTitle string
	root              SessionKey
	lines             *ModelLimit
}

func (f limitFact) money() LimitMoney {
	return LimitMoney{Official: float64(f.row.Costs[0]+f.row.Costs[1]+f.row.Costs[2]) / 1e6, Channel: float64(f.row.BilledCosts[0]+f.row.BilledCosts[1]+f.row.BilledCosts[2]) / 1e6, ChannelUnknown: f.row.UnconfirmedChannel > 0}
}

type limitSample struct {
	money    LimitMoney
	baseline *float64
	group    string
}

func judgeLimits(over, rest []limitSample) (*float64, *LimitMoney) {
	if len(over) == 0 {
		empty := &LimitMoney{}
		for _, sample := range rest {
			empty.ChannelUnknown = empty.ChannelUnknown || sample.money.ChannelUnknown
		}
		return nil, empty
	}
	type average struct {
		money LimitMoney
		count int
	}
	means := map[string]average{}
	for _, v := range rest {
		avg := means[v.group]
		avg.money.add(v.money)
		avg.count++
		means[v.group] = avg
	}
	var spend LimitMoney
	var denominator, total float64
	for _, v := range over {
		avg := means[v.group]
		base := avg.money
		if avg.count > 0 {
			base.Official /= float64(avg.count)
			base.Channel /= float64(avg.count)
		}
		if avg.count == 0 {
			if v.baseline == nil {
				return nil, nil
			}
			base.Official = *v.baseline
			base.Channel = base.Official
			if v.money.Official > 0 {
				base.Channel *= v.money.Channel / v.money.Official
			}
		}
		denominator += base.Official
		total += v.money.Official
		spend.Official += v.money.Official - base.Official
		spend.Channel += v.money.Channel - base.Channel
		spend.ChannelUnknown = spend.ChannelUnknown || v.money.ChannelUnknown || base.ChannelUnknown
	}
	if denominator <= 0 {
		return nil, nil
	}
	multiple := total / denominator
	return &multiple, &spend
}
func limitRow(id, cli, session, title string, facts []limitFact) LimitRow {
	v := LimitRow{ID: id, CLI: cli, Session: session, Title: title}
	if len(facts) == 0 {
		return v
	}
	latest := facts[len(facts)-1]
	for i := len(facts) - 1; i >= 0; i-- {
		if facts[i].agent == "" {
			latest = facts[i]
			break
		}
	}
	v.Model, v.Effort, v.Lines = latest.row.Model, latest.row.Effort, latest.lines
	counts := map[string]int64{}
	lineMap := map[string]*ModelLimit{}
	var over, rest, flatOver, flatRest []limitSample
	var flatCost LimitMoney
	var flatN int64
	hasLimit := false
	hasFlat := false
	for _, f := range facts {
		counts[f.row.Model]++
		lineMap[f.row.Model] = f.lines
		band := limitBand(f.row.Input, f.lines)
		v.Bands[band].Requests++
		if f.row.PricedRequests == 0 {
			v.Unpriced++
			continue
		}
		money := f.money()
		v.Bands[band].PricedRequests++
		v.Bands[band].Money.add(money)
		if f.lines == nil {
			continue
		}
		hasLimit = true
		sample := f.sample()
		switch band {
		case 2:
			over = append(over, sample)
		case 0:
			rest = append(rest, sample)
		}
		if f.lines.FlatLimit == f.lines.Limit {
			continue
		}
		hasFlat = true
		if strings.HasPrefix(f.row.Model, "gpt-") && f.row.Input > 272000 {
			sample.money.Official /= 2
			sample.money.Channel /= 2
		}
		if f.row.Input > f.lines.FlatLimit {
			flatOver = append(flatOver, sample)
			flatN++
			flatCost.add(sample.money)
		} else if f.row.Input <= f.lines.FlatWarn {
			flatRest = append(flatRest, sample)
		}
	}
	if hasLimit {
		v.Multiple, v.OverSpend = judgeLimits(over, rest)
	}
	if hasFlat {
		_, spend := judgeLimits(flatOver, flatRest)
		v.Flat = &LimitFlat{Limit: 350000, Requests: flatN, Cost: flatCost, OverSpend: spend}
	}
	if len(counts) > 1 {
		for model, n := range counts {
			v.ModelLines = append(v.ModelLines, LimitModelLine{model, lineMap[model], n})
		}
		sort.Slice(v.ModelLines, func(i, j int) bool {
			a, b := v.ModelLines[i], v.ModelLines[j]
			if a.Requests != b.Requests {
				return a.Requests > b.Requests
			}
			return a.Model < b.Model
		})
	}
	return v
}
func sortLimitRows(rows []LimitRow) {
	spend := func(v LimitRow) float64 {
		if v.OverSpend != nil {
			return v.OverSpend.Official
		}
		return 0
	}
	share := func(v LimitRow) float64 {
		total := v.Bands[0].Money.Official + v.Bands[1].Money.Official + v.Bands[2].Money.Official
		if total > 0 {
			return v.Bands[2].Money.Official / total
		}
		return 0
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if spend(a) != spend(b) {
			return spend(a) > spend(b)
		}
		if share(a) != share(b) {
			return share(a) > share(b)
		}
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		return a.ID < b.ID
	})
}

// Other families retain their existing pooled comparison; DeepSeek matches model and tariff period.
func (f limitFact) sample() limitSample {
	v := limitSample{money: f.money(), baseline: f.lines.Baseline}
	if strings.HasPrefix(f.row.Model, "deepseek") {
		v.group = limitBaselineKey(f.row.Model, f.row.At)
	}
	return v
}
