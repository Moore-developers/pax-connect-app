package aiusage

func limitRatio(value, base float64) *float64 {
	if base <= 0 {
		return nil
	}
	v := value / base
	return &v
}
func limitTrend(facts []limitFact) LimitTrend {
	out := LimitTrend{}
	turns := map[string]int64{}
	var peak int64
	for i, f := range facts {
		r := LimitRequest{Request: f.row, Lines: f.lines, Band: limitBand(f.row.Input, f.lines)}
		if f.row.Turn != "" {
			n, ok := turns[f.row.Turn]
			if !ok {
				n = int64(len(turns))
				turns[f.row.Turn] = n
			}
			r.TurnNumber = &n
		}
		out.Requests = append(out.Requests, r)
		out.HasWriteLayer = out.HasWriteLayer || f.row.Write > 0
		peak = max(peak, f.row.Input)
		if f.lines != nil {
			index := int64(i)
			if out.OverStart == nil && f.row.Input > f.lines.Limit {
				out.OverStart = &index
			}
			if out.FlatStart == nil && f.lines.FlatLimit != f.lines.Limit && f.row.Input > f.lines.FlatLimit {
				out.FlatStart = &index
			}
		}
		if i == 0 {
			continue
		}
		prev := facts[i-1]
		change := LimitSwitch{Index: int64(i)}
		if f.row.Model != "" && prev.row.Model != "" && f.row.Model != "unknown" && prev.row.Model != "unknown" && f.row.Model != prev.row.Model {
			change.Kind, change.From, change.To = "model", prev.row.Model, f.row.Model
		} else if f.row.Model == prev.row.Model && f.row.Effort != "" && prev.row.Effort != "" && f.row.Effort != prev.row.Effort {
			change.Kind, change.From, change.To = "effort", prev.row.Effort, f.row.Effort
		} else {
			continue
		}
		if f.row.PricedRequests > 0 && prev.row.PricedRequests > 0 {
			change.TurnMultiple = limitRatio(f.money().Official, prev.money().Official)
		}
		// Use the nearest requests with cache reads. An unpriced nearest request is unknown;
		// do not silently skip it and compare a different model's price.
		var before, after *limitFact
		for j := i - 1; j >= 0; j-- {
			if facts[j].row.Read > 0 {
				before = &facts[j]
				break
			}
		}
		for j := i; j < len(facts); j++ {
			if facts[j].row.Read > 0 {
				after = &facts[j]
				break
			}
		}
		if before != nil && after != nil && before.row.PricedRequests > 0 && after.row.PricedRequests > 0 {
			change.AfterReadMultiple = limitRatio(float64(after.row.Costs[1])/float64(after.row.Read), float64(before.row.Costs[1])/float64(before.row.Read))
		}
		out.Switches = append(out.Switches, change)
	}
	if len(facts) > 0 {
		line := facts[len(facts)-1].lines
		if line != nil && peak >= line.Warn {
			out.Lines = line
		}
	}
	if out.Lines == nil {
		out.OverStart = nil
		out.FlatStart = nil
	}
	return out
}
