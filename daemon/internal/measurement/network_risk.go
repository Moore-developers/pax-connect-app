package measurement

import (
	"math"
	"strconv"
)

// Distinct vendor numbers remain attributed. Missing or invalid fields are
// unknown and never turned into zero risk or a combined score.
func appendRawNetworkRisk(value *NetworkDiagnosticReport, raw ipxReport) {
	add := func(label, text, source, state string) {
		value.Facts = append(value.Facts, NetworkDiagnosticFact{Group: "raw_risk", Label: label, Value: diagnosticText(text), Source: source, State: state, ProviderID: "ipx.al"})
	}
	risk := func(label, source string, number *float64) {
		if number == nil || math.IsNaN(*number) || math.IsInf(*number, 0) || *number < 0 || *number > 100 {
			add(label, "未返回有效数值", source, "unknown")
			return
		}
		add(label, strconv.FormatFloat(*number, 'f', -1, 64), source, "information")
	}
	var proxy, query, abuse *float64
	if raw.Risk.Proxycheck != nil {
		proxy = raw.Risk.Proxycheck.Risk
	}
	if raw.Risk.IPQuery != nil {
		query = raw.Risk.IPQuery.Risk
	}
	if raw.Risk.AbuseIPDB != nil {
		abuse = raw.Risk.AbuseIPDB.Score
	}
	risk("proxycheck 风险", "proxycheck", proxy)
	if raw.Risk.Proxycheck != nil && raw.Risk.Proxycheck.VPN != nil {
		text := "否"
		state := "clear"
		if *raw.Risk.Proxycheck.VPN {
			text = "是"
			state = "hit"
		}
		add("proxycheck VPN", text, "proxycheck", state)
	} else {
		add("proxycheck VPN", "未返回", "proxycheck", "unknown")
	}
	risk("IPQuery 风险", "IPQuery", query)
	risk("AbuseIPDB 置信度", "AbuseIPDB", abuse)
	if raw.Risk.AbuseIPDB != nil && raw.Risk.AbuseIPDB.Reports != nil && *raw.Risk.AbuseIPDB.Reports >= 0 {
		add("AbuseIPDB 举报数", strconv.Itoa(*raw.Risk.AbuseIPDB.Reports), "AbuseIPDB", "information")
	} else {
		add("AbuseIPDB 举报数", "未返回", "AbuseIPDB", "unknown")
	}
	if raw.Risk.AbuseIPDB != nil && raw.Risk.AbuseIPDB.Last != "" {
		add("最近举报", raw.Risk.AbuseIPDB.Last, "AbuseIPDB", "information")
	}
}
