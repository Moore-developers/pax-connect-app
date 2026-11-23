package aiusage

import "time"

const antigravityPriceVersion = "2026-11-22-google-cloud-global"

// AntigravityPrice is the published Google Cloud standard Global reference rate
// in USD per million tokens. CacheWrite is absent until the published start date.
type AntigravityPrice struct {
	Model                    string
	From                     time.Time
	Input, CacheRead, Output float64
	CacheWrite               *float64
}

var (
	december2026       = time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	january2027        = time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	agyCacheWriteStart = time.Date(2027, 1, 12, 0, 0, 0, 0, time.UTC)
)

// AntigravityPrices returns the five supported model references at the given
// instant. The promotional periods follow Google's published pricing dates.
func AntigravityPrices(at time.Time) []AntigravityPrice {
	carbon, flash := 2.0, 0.75
	if !at.Before(december2026) {
		carbon = 4
	}
	if !at.Before(january2027) {
		flash = 1.5
	}
	rows := []AntigravityPrice{
		{Model: "gemini-4-neon", Input: 3, CacheRead: .15, Output: 15},
		{Model: "gemini-4-carbon", Input: carbon, CacheRead: carbon * .05, Output: carbon * 5},
		{Model: "gemini-4-argon", Input: carbon, CacheRead: carbon * .05, Output: carbon * 5},
		{Model: "gemini-4-hydrogen", Input: 1.5, CacheRead: .075, Output: 7.5},
		{Model: "gemini-3.8-flash", Input: flash, CacheRead: flash * .1, Output: flash * 5},
	}
	if !at.Before(december2026) {
		rows[1].From, rows[2].From = december2026, december2026
	}
	if !at.Before(january2027) {
		rows[4].From = january2027
	}
	if !at.Before(agyCacheWriteStart) {
		neon, hydrogen := 3.75, 1.875
		rows[0].CacheWrite, rows[3].CacheWrite = &neon, &hydrogen
		rows[0].From, rows[3].From = agyCacheWriteStart, agyCacheWriteStart
	}
	return rows
}

// AntigravityPriceSchedule includes current and already published upcoming rates.
func AntigravityPriceSchedule(at time.Time) []AntigravityPrice {
	rows := AntigravityPrices(at)
	for _, start := range []time.Time{december2026, january2027, agyCacheWriteStart} {
		if start.After(at) {
			for _, rate := range AntigravityPrices(start) {
				if rate.From.Equal(start) {
					rows = append(rows, rate)
				}
			}
		}
	}
	return rows
}

func (r *Request) antigravityTariff() (tariff, string) {
	for _, p := range AntigravityPrices(time.UnixMilli(r.At)) {
		if p.Model != r.Model {
			continue
		}
		var write float64
		if p.CacheWrite != nil {
			write = *p.CacheWrite
		} else if r.Write != 0 {
			return tariff{}, ""
		}
		return tariff{p.Input, p.CacheRead, write, write, p.Output}, antigravityPriceVersion
	}
	return tariff{}, ""
}
