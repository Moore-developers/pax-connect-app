package aiusage

import (
	"fmt"
	"sort"
)

func sessionLimitID(k SessionKey) string { return k.CLI + ":" + k.Session }
func limitTitle(g agentGraph, k SessionKey) string {
	if title := g.Catalog[k].Title; title != "" {
		if g.Catalog[k].TitleSource == "claude.last-prompt" || g.Catalog[k].TitleSource == "claude.user-message" {
			return "最近消息：" + title
		}
		return title
	}
	id := k.Session
	if len(id) > 12 {
		id = id[:12]
	}
	return "未命名会话 · " + id
}
func limitModelRows(facts []limitFact, prefix string) []LimitRow {
	groups := map[string][]limitFact{}
	sessions := map[string]map[SessionKey]bool{}
	for _, f := range facts {
		k := f.row.Model + "\x1f" + f.row.Effort
		groups[k] = append(groups[k], f)
		if sessions[k] == nil {
			sessions[k] = map[SessionKey]bool{}
		}
		sessions[k][f.root] = true
	}
	var rows []LimitRow
	for k, fs := range groups {
		v := limitRow(prefix+k, fs[0].row.CLI, "", fs[0].row.Model, fs)
		if prefix == "m:" {
			v.Note = fmt.Sprintf("%d 个会话", len(sessions[k]))
		}
		rows = append(rows, v)
	}
	sortLimitRows(rows)
	return rows
}
func limitFactInScope(f limitFact, g agentGraph, selected *SessionKey) bool {
	if selected == nil {
		return true
	}
	for _, key := range g.scopes(SessionKey{f.row.CLI, f.row.Session}) {
		if key == *selected {
			return true
		}
	}
	return false
}
func assembleLimits(facts []limitFact, g agentGraph, active map[agentKey]bool, selected *SessionKey) *SessionLimits {
	out := &SessionLimits{AgentsIncomplete: g.Incomplete}
	out.Summary.TurnsComplete = true
	sessions := map[SessionKey][]limitFact{}
	turns := map[contextTurn]bool{}
	agents := map[string][]limitFact{}
	var scoped, main []limitFact
	var safeSamples, overSamples []limitSample
	for _, f := range facts {
		source := SessionKey{f.row.CLI, f.row.Session}
		if !limitFactInScope(f, g, selected) {
			continue
		}
		// A directly selected native child is its own main line. Only its descendants
		// remain subagents; Claude's same-session request mapping still applies.
		if selected != nil && source == *selected && source.CLI != "claude" {
			f.agent = ""
		}
		sessions[f.root] = append(sessions[f.root], f)
		scoped = append(scoped, f)
		if f.agent != "" {
			agents[f.agent] = append(agents[f.agent], f)
		} else {
			main = append(main, f)
		}
		out.Summary.Requests++
		out.Summary.InputTokens += f.row.Input
		if f.lines != nil {
			band := &out.Summary.Bands[limitBand(f.row.Input, f.lines)]
			band.Requests++
			if f.row.PricedRequests > 0 {
				band.PricedRequests++
				band.Money.add(f.money())
				sample := f.sample()
				sample.baseline = nil // Summary compares observed safe samples only.
				switch limitBand(f.row.Input, f.lines) {
				case 0:
					safeSamples = append(safeSamples, sample)
				case 2:
					overSamples = append(overSamples, sample)
				}
			}
		}
		if f.row.Turn == "" {
			out.Summary.TurnsComplete = false
		} else {
			turns[contextTurn{f.row.CLI, f.row.Session, f.row.Turn}] = true
		}
		if f.row.PricedRequests > 0 {
			money := f.money()
			out.Summary.Input.add(money)
			out.Summary.Curve = append(out.Summary.Curve, money.Official)
		}
	}
	out.Summary.Multiple, _ = judgeLimits(overSamples, safeSamples)
	out.Summary.Turns = int64(len(turns))
	out.Summary.Sessions = int64(len(sessions))
	var totalRows []LimitRow
	for k, fs := range sessions {
		v := limitRow("s:"+sessionLimitID(k), k.CLI, k.Session, limitTitle(g, k), fs)
		totalRows = append(totalRows, v)
	}
	if selected != nil {
		totalRows = []LimitRow{limitRow("s:"+sessionLimitID(*selected), selected.CLI, selected.Session, limitTitle(g, *selected), scoped)}
		if len(scoped) > 0 {
			out.Summary.Sessions = 1
		}
	}
	for _, row := range totalRows {
		if row.Bands[2].Requests > 0 {
			out.Summary.OverSessions++
		}
		if row.OverSpend != nil {
			out.Summary.OverSpend.add(*row.OverSpend)
		}
	}
	// Native activity includes agents with no priced/request records; retain explicit empty rows.
	for key := range active {
		a, ok := g.Agents[key]
		if !ok {
			continue
		}
		included := selected == nil
		if selected != nil {
			for _, owner := range g.owners(a) {
				if owner == *selected {
					included = true
					break
				}
			}
		}
		if !included {
			continue
		}
		id := key.CLI + ":" + key.Session
		if key.CLI == "claude" {
			id += ":" + key.ID
		}
		if _, ok := agents[id]; !ok {
			agents[id] = nil
		}
		out.AgentsIncomplete = out.AgentsIncomplete || g.incomplete(a)
	}
	out.Summary.Subagents = int64(len(agents))
	if selected == nil {
		out.Primary = totalRows
		activity := g.lastActivity()
		for _, fact := range scoped {
			activity[fact.root] = max(activity[fact.root], fact.row.At)
		}
		sort.Slice(out.Primary, func(i, j int) bool {
			a, b := out.Primary[i], out.Primary[j]
			at, bt := activity[SessionKey{a.CLI, a.Session}], activity[SessionKey{b.CLI, b.Session}]
			if at != bt {
				return at > bt
			}
			if a.CLI != b.CLI {
				return a.CLI < b.CLI
			}
			return a.Session < b.Session
		})
		out.Secondary = limitModelRows(scoped, "m:")
		return out
	}
	total := limitRow("main", selected.CLI, "", "主线路", main)
	total.Emphasized = true
	out.Primary = []LimitRow{total}
	models := limitModelRows(main, "main:")
	if len(models) > 1 {
		out.Primary = append(out.Primary, models...)
	}
	for id, fs := range agents {
		title := id
		if len(fs) > 0 {
			title = fs[len(fs)-1].agentTitle
		} else {
			for k := range active {
				if id == k.CLI+":"+k.Session || id == k.CLI+":"+k.Session+":"+k.ID {
					title = g.Names[k]
					if title == "" {
						title = k.ID
					}
					if t := g.Catalog[SessionKey{k.CLI, k.Session}].Title; t != "" && k.CLI != "claude" {
						title = t
					}
					break
				}
			}
		}
		v := limitRow("a:"+id, selected.CLI, "", title, fs)
		out.Secondary = append(out.Secondary, v)
	}
	sortLimitRows(out.Secondary)
	return out
}
