package measurement

import (
	"context"
	"net/netip"
	"slices"
	"sync"
	"time"
)

var riskCapabilities = []struct{ label, capability string }{
	{"代理", "risk.proxy"}, {"VPN", "risk.vpn"}, {"Tor", "risk.tor"},
	{"机房 IP", "risk.datacenter"}, {"移动网络", "risk.mobile"},
	{"开放端口", "exposure.ports"}, {"已知漏洞", "exposure.vulns"}, {"Shodan 标签", "exposure.tags"},
	{"iCloud 中继", "risk.relay"}, {"匿名出口", "risk.anonymous"}, {"滥用标记", "risk.abuse"},
	{"代理风险 ≥67", "risk.proxy_score"}, {"爬虫", "risk.crawler"}, {"威胁情报名单", "risk.threat"},
	{"恶意扫描", "risk.scanner"}, {"邮件 DNSBL", "risk.dnsbl"}, {"自签证书", "exposure.certificate"},
	{"伪家宽", "risk.residential"}, {"人类流量 <50%", "risk.human"}, {"广播 IP", "risk.broadcast"},
}

// DiagnoseIP collects independent exact-IP records first, then fills uncovered
// checks from one bounded ipx.al report. Valid direct evidence always wins.
func (q *ExitLookup) DiagnoseIP(ctx context.Context, client Client, ip, scope string) NetworkDiagnosticReport {
	addr, err := netip.ParseAddr(ip)
	if err != nil || !addr.IsGlobalUnicast() || addr.IsPrivate() {
		return NetworkDiagnosticReport{ProviderError: "exit_unavailable"}
	}
	addr = addr.Unmap()
	ip = addr.String()
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	primary, done := context.WithTimeout(bounded, 8*time.Second)
	results := make([]intelligenceResult, 3)
	var workers sync.WaitGroup
	for i, id := range []string{"ipquery", "ipwho.is", "internetdb"} {
		workers.Add(1)
		go func(i int, id string) {
			defer workers.Done()
			if id == "internetdb" && addr.Is6() {
				results[i].attempt = NetworkDiagnosticProvider{ID: id, QueryIP: ip, Family: "ipv6", State: "unsupported", ErrorCode: "provider_family_unsupported", At: time.Now()}
				return
			}
			results[i] = q.fetchIntelligence(primary, client, id, ip, scope)
		}(i, id)
	}
	workers.Wait()
	report := NetworkDiagnosticReport{}
	for _, result := range results {
		appendIntelligence(&report, result)
	}
	// Core location has two additional independent candidates; a missing city never
	// spends fallback capacity. Each of these requests shares exit-query quotas.
	for _, id := range []string{"ip.sb", "geojs"} {
		if primary.Err() != nil || (report.Country != "" && hasASN(report)) {
			break
		}
		appendIntelligence(&report, q.fetchIntelligence(primary, client, id, ip, scope))
	}
	done()
	missing := missingIntelligence(report)
	if len(missing) > 0 && bounded.Err() == nil {
		fallback := q.fetchIntelligence(bounded, client, "ipx.al", ip, scope)
		fallback.attempt.FallbackFor = missing
		fallback.attempt.FallbackReason = "independent_coverage_missing"
		applyFallback(&report, fallback, missing)
	}
	addUnmeasuredRisks(&report)
	usable := len(report.Sources) > 0
	for _, f := range report.Facts {
		usable = usable || (f.Group == "risk" && f.State != "unknown")
	}
	for _, p := range report.Providers {
		usable = usable || p.State == "not_indexed"
	}
	if !usable {
		report.ProviderError = "intelligence_unavailable"
	}
	return report
}

func appendIntelligence(report *NetworkDiagnosticReport, result intelligenceResult) {
	attempt := result.attempt
	if attempt.State == "observed" {
		attempt.Capabilities = intelligenceCapabilities(result.report)
		expected := 2
		if attempt.ID == "ipquery" {
			expected = 7
		}
		if attempt.ID == "internetdb" {
			expected = 3
		}
		if len(attempt.Capabilities) < expected {
			attempt.State = "partial"
			attempt.ErrorCode = "provider_fields_missing"
		}
	}
	report.Providers = append(report.Providers, attempt)
	report.Sources = append(report.Sources, result.report.Sources...)
	report.Facts = append(report.Facts, result.report.Facts...)
	if report.Country == "" {
		report.Country = result.report.Country
	}
	if report.Timezone == "" {
		report.Timezone = result.report.Timezone
	}
	if report.IPType == "" {
		report.IPType = result.report.IPType
	}
}
func intelligenceCapabilities(report NetworkDiagnosticReport) []string {
	var caps []string
	if report.Country != "" {
		caps = append(caps, "geo.country")
	}
	if hasASN(report) {
		caps = append(caps, "geo.asn")
	}
	for _, item := range riskCapabilities {
		if hasRisk(report, item.label) {
			caps = append(caps, item.capability)
		}
	}
	return caps
}
func hasASN(report NetworkDiagnosticReport) bool {
	for _, s := range report.Sources {
		if s.ASN != "" {
			return true
		}
	}
	return false
}
func hasRisk(report NetworkDiagnosticReport, label string) bool {
	for _, f := range report.Facts {
		if f.Group == "risk" && f.Label == label && (f.State == "hit" || f.State == "clear" || f.State == "information") {
			return true
		}
	}
	return false
}
func missingIntelligence(report NetworkDiagnosticReport) []string {
	caps := intelligenceCapabilities(report)
	wanted := []string{"geo.country", "geo.asn"}
	for _, item := range riskCapabilities {
		wanted = append(wanted, item.capability)
	}
	var missing []string
	for _, capability := range wanted {
		if !slices.Contains(caps, capability) {
			missing = append(missing, capability)
		}
	}
	return missing
}
func applyFallback(report *NetworkDiagnosticReport, result intelligenceResult, missing []string) {
	attempt := result.attempt
	attempt.Capabilities = intelligenceCapabilities(result.report)
	report.Providers = append(report.Providers, attempt)
	if result.attempt.State != "observed" {
		return
	}
	// Keep the original full multi-database comparison as attributed supplemental
	// rows, even when the independent location is already available.
	report.Sources = append(report.Sources, result.report.Sources...)
	if report.Country == "" {
		report.Country = result.report.Country
	}
	if report.Timezone == "" {
		report.Timezone = result.report.Timezone
	}
	for _, fact := range result.report.Facts {
		include := false
		switch fact.Group {
		case "risk", "exposure_detail":
			for _, item := range riskCapabilities {
				if fact.Label == item.label && slices.Contains(missing, item.capability) {
					include = true
				}
			}
		case "dnsbl":
			include = slices.Contains(missing, "risk.dnsbl")
		case "raw_risk", "registration", "network", "provider", "type":
			include = fact.Value != ""
		}
		if !include {
			continue
		}
		// Supplier numbers and metadata use their own attribution, but cannot replace
		// an already-observed value for the same fact (including a valid zero).
		existing := slices.ContainsFunc(report.Facts, func(f NetworkDiagnosticFact) bool {
			return f.Group == fact.Group && f.Label == fact.Label
		})
		if !existing {
			report.Facts = append(report.Facts, fact)
		}
	}

	if report.IPType == "" {
		report.IPType = result.report.IPType
	}
	// Supplier score stays ancillary; it never replaces the primary risk summary.
	report.Score = result.report.Score
	report.ScoreModel = result.report.ScoreModel
	report.ScoreCeiling = result.report.ScoreCeiling
}
func addUnmeasuredRisks(report *NetworkDiagnosticReport) {
	for _, label := range []string{"代理", "VPN", "Tor", "iCloud 中继", "匿名出口", "滥用标记", "代理风险 ≥67", "爬虫", "威胁情报名单", "恶意扫描", "邮件 DNSBL", "Shodan 标签", "开放端口", "已知漏洞", "自签证书", "机房 IP", "移动网络", "伪家宽", "人类流量 <50%", "广播 IP"} {
		found := false
		for _, fact := range report.Facts {
			if fact.Group == "risk" && fact.Label == label {
				found = true
				break
			}
		}
		if found {
			continue
		}
		source := "独立与补充来源均未提供"
		report.Facts = append(report.Facts, NetworkDiagnosticFact{Group: "risk", Label: label, Value: riskCategory(label), Source: source, State: "unknown"})
	}
}
