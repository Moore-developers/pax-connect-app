package aiusage

import "time"

// deepseekTariff follows DeepSeek's published rule: peak hours are 01:00-04:00 and
// 06:00-10:00 UTC on weekdays; weekends and Chinese statutory holidays are off-peak all
// day. Leave and make-up days around holidays are not statutory holidays, so they
// follow the plain weekday rule. Statutory dates are verified for 2026 only
// (tests/ai-usage/deepseek-2026-holiday-sources.md); other years use the weekday rule.
func deepseekTariff(at int64) tariff {
	t := time.UnixMilli(at).UTC()
	local := t.In(time.FixedZone("Asia/Shanghai", 8*3600))
	off := tariff{.15, .003, 0, 0, .6}
	peak := t.Hour() >= 1 && t.Hour() < 4 || t.Hour() >= 6 && t.Hour() < 10
	if !peak || local.Weekday() == time.Saturday || local.Weekday() == time.Sunday {
		return off
	}
	if local.Year() == 2026 && statutory2026[local.Format("01-02")] {
		return off
	}
	return tariff{.3, .006, 0, 0, 1.2}
}

var statutory2026 = map[string]bool{"01-01": true, "02-16": true, "02-17": true, "02-18": true, "02-19": true, "04-05": true, "05-01": true, "05-02": true, "06-19": true, "09-25": true, "10-01": true, "10-02": true, "10-03": true}
