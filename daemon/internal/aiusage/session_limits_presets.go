package aiusage

import (
	"context"
	"database/sql"
	"strings"
)

// Presets live beside prices.go. Changes require user approval (limits spec §3).
func modelLimit(model string) *ModelLimit {
	v := &ModelLimit{Model: model}
	var base float64
	switch model {
	case "gpt-6.1-sol":
		v.Limit, v.Warn, v.FlatLimit, v.FlatWarn, base = 272000, 250000, 350000, 300000, .023
	case "gpt-6.1-terra":
		v.Limit, v.Warn, v.FlatLimit, v.FlatWarn, base = 272000, 250000, 350000, 300000, .009
	case "gpt-6-astra":
		v.Limit, v.Warn, base = 200000, 150000, .137
	case "claude-opus-5-5":
		v.Limit, v.Warn, base = 200000, 150000, .069
	case "claude-fable-5-2":
		v.Limit, v.Warn, base = 200000, 150000, .12
	case "claude-sonnet-5-5":
		v.Limit, v.Warn, base = 250000, 200000, .025
	case "claude-haiku-5-5":
		v.Limit, v.Warn, base = 100000, 80000, .0011
	case "deepseek-flash":
		v.Limit, v.Warn = 300000, 250000
	default:
		if strings.HasPrefix(model, "gpt-") {
			v.Limit, v.Warn = 272000, 250000
		} else if strings.HasPrefix(model, "claude-") {
			v.Limit, v.Warn = 200000, 150000
		} else if strings.HasPrefix(model, "deepseek") {
			v.Limit, v.Warn = 300000, 250000
		} else {
			return nil
		}
	}
	if v.FlatLimit == 0 {
		v.FlatLimit, v.FlatWarn = v.Limit, v.Warn
	}
	if base > 0 {
		v.Baseline = &base
	}
	return v
}
func limitBand(input int64, line *ModelLimit) int32 {
	if line == nil || input <= line.Warn {
		return 0
	}
	if input <= line.Limit {
		return 1
	}
	return 2
}
func readLimitBaselines(ctx context.Context, tx *sql.Tx, retained int64) (map[string]*ModelLimit, error) {
	lines := map[string]*ModelLimit{}
	// DeepSeek timestamps are grouped in Go through the same peak/holiday rule as pricing.
	rows, err := tx.QueryContext(ctx, `SELECT model,CASE WHEN model LIKE 'deepseek%' THEN at ELSE 0 END,count(*),sum(c0+c1+c2)/1000000.0 FROM requests WHERE at>=? AND input<=CASE WHEN model='claude-haiku-5-5' THEN 100000 ELSE 150000 END AND json_extract(details,'$.Prices[0]') IS NOT NULL AND model<>'unknown' GROUP BY model,CASE WHEN model LIKE 'deepseek%' THEN at ELSE 0 END`, retained)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	type total struct {
		count int64
		money float64
		model string
	}
	totals := map[string]total{}
	for rows.Next() {
		var model string
		var at, n int64
		var money float64
		if err = rows.Scan(&model, &at, &n, &money); err != nil {
			return nil, err
		}
		key := limitBaselineKey(model, at)
		v := totals[key]
		v.count += n
		v.money += money
		v.model = model
		totals[key] = v
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for key, v := range totals {
		if line := modelLimit(v.model); line != nil {
			if v.count >= 50 {
				mean := v.money / float64(v.count)
				line.Baseline = &mean
			}
			lines[key] = line
		}
	}
	return lines, nil
}

func limitBaselineKey(model string, at int64) string {
	if !strings.HasPrefix(model, "deepseek") {
		return model
	}
	if deepseekTariff(at).input == .3 {
		return model + "\x1fpeak"
	}
	return model + "\x1foff-peak"
}
