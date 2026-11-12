package measurement

import (
	"encoding/json"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const networkReportLimit = 500 * 1024

type ipxSource struct {
	Name, Country, City string
	ASN                 json.Number
	Org, Date           string
}
type ipxCheck struct {
	Label string
	Hit   *bool
}
type ipxReport struct {
	GeneratedAt string
	IP          string
	Location    struct{ Country, Timezone string }
	Health      struct {
		Score   *int
		Model   string
		Ceiling int
		Items   []struct {
			Label string
			Pts   int
			Src   string
			Note  string
		}
		Coverage struct {
			Intel, IntelTotal int
			Low               bool
		}
	}
	Type struct {
		Verdict string
		Votes   []struct{ Src, V string }
	}
	Native     struct{ Verdict, RegCountry, UseCountry, RegSrc, RegWhois, RegRir string }
	Sources    []ipxSource
	Categories []struct {
		Label  string
		Checks []ipxCheck
	}
	Routing struct {
		Prefix, RPKI string
		OriginASN    json.Number
	}
	Risk struct {
		Proxycheck *struct {
			Risk *float64
			VPN  *bool
		}
		IPQuery   *struct{ Risk *float64 }
		AbuseIPDB *struct {
			Score   *float64
			Reports *int
			Last    string
		}
	}
	Exposure struct {
		Ports []int
		Tags  []string
		Vulns *int
	}
	Network struct {
		HumanPct  *float64
		RadarDate string
	}
	Hostname string
	DNSBL    struct {
		List []struct{ Name, Status string }
	}
}

func parseNetworkReport(body []byte, expected netip.Addr) NetworkDiagnosticReport {
	invalid := NetworkDiagnosticReport{ProviderError: "provider_response_invalid"}
	var raw ipxReport
	if json.Unmarshal(body, &raw) != nil {
		return invalid
	}
	ip, err := netip.ParseAddr(raw.IP)
	if err != nil || ip.Unmap() != expected.Unmap() {
		return NetworkDiagnosticReport{ProviderError: "provider_ip_mismatch"}
	}
	if raw.Health.Ceiling < 0 || raw.Health.Ceiling > 100 || (raw.Health.Score != nil && (*raw.Health.Score < 0 || *raw.Health.Score > 100)) {
		return invalid
	}
	if len(raw.Sources) > 32 || len(raw.Health.Items) > 64 || len(raw.Categories) > 16 || len(raw.Type.Votes) > 32 {
		return invalid
	}
	if raw.Health.Coverage.Intel < 0 || raw.Health.Coverage.IntelTotal < 0 || raw.Health.Coverage.Intel > raw.Health.Coverage.IntelTotal || raw.Health.Coverage.IntelTotal > 100 {
		return invalid
	}
	value := NetworkDiagnosticReport{Score: raw.Health.Score, ScoreModel: diagnosticText(raw.Health.Model), ScoreCeiling: raw.Health.Ceiling, IPType: diagnosticText(raw.Type.Verdict), Country: diagnosticText(raw.Location.Country), IntelAnswered: raw.Health.Coverage.Intel, IntelTotal: raw.Health.Coverage.IntelTotal}
	if _, err := time.LoadLocation(raw.Location.Timezone); raw.Location.Timezone != "" && err == nil {
		value.Timezone = diagnosticText(raw.Location.Timezone)
	}
	for _, source := range raw.Sources {
		value.Sources = append(value.Sources, NetworkDiagnosticSource{Name: diagnosticText(source.Name), Country: diagnosticText(source.Country), City: diagnosticText(source.City), ASN: diagnosticText(string(source.ASN)), Org: diagnosticText(source.Org), ProviderDate: diagnosticText(source.Date), ProviderID: "ipx.al"})
	}
	add := func(group, label, text, source, state string) {
		value.Facts = append(value.Facts, NetworkDiagnosticFact{Group: group, Label: diagnosticText(label), Value: diagnosticText(text), Source: diagnosticText(source), State: state, ProviderID: "ipx.al"})
	}
	for _, item := range raw.Health.Items {
		text := strconv.Itoa(item.Pts) + " 分"
		if item.Note != "" && item.Note != "pass" {
			text += " · " + item.Note
		}
		add("score", item.Label, text, item.Src, "information")
	}
	for _, vote := range raw.Type.Votes {
		add("type", "类型判断", vote.V, vote.Src, "information")
	}
	for _, category := range raw.Categories {
		if len(category.Checks) > 32 {
			return invalid
		}
		for _, check := range category.Checks {
			state := "unknown"
			if check.Hit != nil {
				state = "clear"
				if *check.Hit {
					state = "hit"
				}
			}
			add("risk", check.Label, category.Label, "ipx.al", state)
		}
	}
	if len(raw.DNSBL.List) > 64 {
		return invalid
	}
	for _, entry := range raw.DNSBL.List {
		state := "unknown"
		if entry.Status == "clean" {
			state = "clear"
		}
		if entry.Status == "listed" {
			state = "hit"
		}
		add("dnsbl", entry.Name, entry.Status, "DNSBL", state)
	}
	add("registration", "注册国家", raw.Native.RegCountry, raw.Native.RegSrc, "information")
	add("registration", "WHOIS 国家", raw.Native.RegWhois, "WHOIS", "information")
	add("registration", "RIR 国家", raw.Native.RegRir, "RIR", "information")
	add("registration", "推定使用国", raw.Native.UseCountry, "ipx.al", "information")
	add("registration", "原生 / 广播", raw.Native.Verdict, "ipx.al", "information")
	add("network", "宣告前缀", raw.Routing.Prefix, "ipx.al", "information")
	add("network", "RPKI", raw.Routing.RPKI, "ipx.al", "information")
	add("network", "rDNS", raw.Hostname, "ipx.al", "information")
	if _, err := time.Parse(time.RFC3339Nano, raw.GeneratedAt); err == nil {
		add("provider", "资料生成时间", raw.GeneratedAt, "ipx.al", "information")
	}
	if len(raw.Exposure.Ports) > 256 || len(raw.Exposure.Tags) > 64 {
		return invalid
	}
	for _, port := range raw.Exposure.Ports {
		if port < 1 || port > 65535 {
			return invalid
		}
	}
	for _, tag := range raw.Exposure.Tags {
		if len(tag) > 180 || diagnosticText(tag) != tag {
			return invalid
		}
	}
	if len(raw.Exposure.Ports) > 0 {
		add("exposure_detail", "开放端口", strings.Join(portStrings(raw.Exposure.Ports), ", "), "Shodan · 经 ipx.al", "information")
	}
	if len(raw.Exposure.Tags) > 0 {
		add("exposure_detail", "Shodan 标签", strings.Join(raw.Exposure.Tags, ", "), "Shodan · 经 ipx.al", "information")
	}
	if raw.Exposure.Vulns != nil {
		if *raw.Exposure.Vulns < 0 {
			return invalid
		}
		add("exposure_detail", "已知漏洞", strconv.Itoa(*raw.Exposure.Vulns)+" 项（供应方计数，未提供逐项CVE）", "Shodan · 经 ipx.al", "information")
	}
	if pct := raw.Network.HumanPct; pct != nil && *pct >= 0 && *pct <= 100 {
		add("raw_risk", "ASN 人类流量比例", strconv.FormatFloat(*pct, 'f', -1, 64)+"%", "ipx.al", "information")
		add("provider", "流量比例资料日期", raw.Network.RadarDate, "ipx.al", "information")
	}
	appendRawNetworkRisk(&value, raw)
	return value
}

func diagnosticText(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
	if len([]rune(s)) > 180 {
		s = string([]rune(s)[:180])
	}
	return s
}
