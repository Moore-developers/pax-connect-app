package measurement

import (
	"context"
	"net/netip"
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

// DiagnoseIP collects direct exact-IP evidence for the current environment.
func (q *ExitLookup) DiagnoseIP(ctx context.Context, client Client, ip, scope string) NetworkDiagnosticReport {
	return q.diagnoseIP(ctx, client, ip, scope, false)
}

// DiagnoseRouteIP adds comparison sources to the specified route's exact-IP lookup.
func (q *ExitLookup) DiagnoseRouteIP(ctx context.Context, client Client, ip, scope string) NetworkDiagnosticReport {
	return q.diagnoseIP(ctx, client, ip, scope, true)
}

func (q *ExitLookup) diagnoseIP(ctx context.Context, client Client, ip, scope string, comparison bool) NetworkDiagnosticReport {
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
	var additional []intelligenceResult
	if comparison {
		additional = make([]intelligenceResult, len(routeComparisonIDs))
		for i, id := range routeComparisonIDs {
			workers.Go(func() {
				additional[i] = q.fetchIntelligence(bounded, client, id, ip, scope)
			})
		}
	}
	workers.Wait()
	for _, result := range additional {
		report.Providers = append(report.Providers, result.attempt)
		report.Sources = append(report.Sources, result.report.Sources...)
		report.Facts = append(report.Facts, result.report.Facts...)
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

func addUnmeasuredRisks(report *NetworkDiagnosticReport) {
	for _, label := range []string{"代理", "VPN", "Tor", "机房 IP", "移动网络", "开放端口", "已知漏洞", "Shodan 标签"} {
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
		source := "直查来源未提供"
		report.Facts = append(report.Facts, NetworkDiagnosticFact{Group: "risk", Label: label, Value: riskCategory(label), Source: source, State: "unknown"})
	}
}
