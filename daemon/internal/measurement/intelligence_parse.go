package measurement

import (
	"encoding/json"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// parseIntelligence retains only bounded, standard evidence from fixed sources.
func parseIntelligence(id string, body []byte, expected netip.Addr) NetworkDiagnosticReport {
	invalid := NetworkDiagnosticReport{ProviderError: "provider_response_invalid"}
	if id == "ipx.al" {
		return parseNetworkReport(body, expected)
	}
	if id == "internetdb" {
		return parseInternetDB(body, expected)
	}
	geo, code := parseGeoProvider(id, body)
	if code != "" {
		return invalid
	}
	observed, err := netip.ParseAddr(geo.IP)
	if err != nil || observed.Unmap() != expected.Unmap() {
		return NetworkDiagnosticReport{ProviderError: "provider_ip_mismatch"}
	}
	country := geo.CountryCode
	report := NetworkDiagnosticReport{Country: country, Sources: []NetworkDiagnosticSource{{Name: id, Country: country, City: geo.City, ASN: geo.ASN, Org: geo.ISP, ProviderID: id}}}
	var raw struct {
		Location struct{ Timezone string }
		Timezone struct{ ID string }
		Risk     struct {
			IsMobile     *bool    `json:"is_mobile"`
			IsVPN        *bool    `json:"is_vpn"`
			IsTor        *bool    `json:"is_tor"`
			IsProxy      *bool    `json:"is_proxy"`
			IsDatacenter *bool    `json:"is_datacenter"`
			Score        *float64 `json:"risk_score"`
		}
	}
	if json.Unmarshal(body, &raw) != nil {
		return invalid
	}
	zone := raw.Location.Timezone
	if id == "ipwho.is" {
		zone = raw.Timezone.ID
	}
	if _, err := time.LoadLocation(zone); zone != "" && err == nil {
		report.Timezone = zone
	}
	if id != "ipquery" {
		return report
	}
	for _, flag := range []struct {
		label string
		value *bool
	}{
		{"代理", raw.Risk.IsProxy}, {"VPN", raw.Risk.IsVPN}, {"Tor", raw.Risk.IsTor}, {"机房 IP", raw.Risk.IsDatacenter}, {"移动网络", raw.Risk.IsMobile},
	} {
		if flag.value == nil {
			continue
		}
		state := "clear"
		if *flag.value {
			state = "hit"
		}
		report.Facts = append(report.Facts, NetworkDiagnosticFact{Group: "risk", Label: flag.label, Value: riskCategory(flag.label), Source: "IPQuery", State: state, ProviderID: id})
	}
	if raw.Risk.IsDatacenter != nil && *raw.Risk.IsDatacenter {
		report.IPType = "hosting"
	}
	if raw.Risk.IsMobile != nil && *raw.Risk.IsMobile {
		report.IPType = "mobile"
	}
	if v := raw.Risk.Score; v != nil && *v >= 0 && *v <= 100 {
		report.Facts = append(report.Facts, NetworkDiagnosticFact{Group: "raw_risk", Label: "IPQuery 风险", Value: strconv.FormatFloat(*v, 'f', -1, 64), Source: "IPQuery", State: "information", ProviderID: id})
	}
	return report
}

func parseInternetDB(body []byte, expected netip.Addr) NetworkDiagnosticReport {
	invalid := NetworkDiagnosticReport{ProviderError: "provider_response_invalid"}
	var raw struct {
		IP          string
		Ports       []int
		Vulns, Tags []string
	}
	if json.Unmarshal(body, &raw) != nil || len(raw.Ports) > 256 || len(raw.Vulns) > 256 || len(raw.Tags) > 64 {
		return invalid
	}
	ip, err := netip.ParseAddr(raw.IP)
	if err != nil || ip.Unmap() != expected.Unmap() {
		return NetworkDiagnosticReport{ProviderError: "provider_ip_mismatch"}
	}
	report := NetworkDiagnosticReport{}
	// A missing array is not the same as an explicitly empty indexed record.
	for _, item := range []struct {
		label   string
		values  []string
		present bool
	}{
		{"开放端口", portStrings(raw.Ports), raw.Ports != nil}, {"已知漏洞", raw.Vulns, raw.Vulns != nil}, {"Shodan 标签", raw.Tags, raw.Tags != nil},
	} {
		if !item.present {
			continue
		}
		state := "clear"
		if len(item.values) > 0 {
			state = "hit"
			if item.label == "Shodan 标签" {
				state = "information"
			}
		}
		report.Facts = append(report.Facts, NetworkDiagnosticFact{Group: "risk", Label: item.label, Value: riskCategory(item.label), Source: "Shodan InternetDB", State: state, ProviderID: "internetdb"})
		for _, value := range item.values {
			if len(value) > 180 || diagnosticText(value) != value {
				return invalid
			}
		}
		if len(item.values) > 0 {
			report.Facts = append(report.Facts, NetworkDiagnosticFact{Group: "exposure_detail", Label: item.label, Value: strings.Join(item.values, ", "), Source: "Shodan InternetDB", State: "information", ProviderID: "internetdb"})
		}
	}
	for _, port := range raw.Ports {
		if port < 1 || port > 65535 {
			return invalid
		}
	}
	return report
}
func portStrings(ports []int) []string {
	result := make([]string, 0, len(ports))
	for _, p := range ports {
		result = append(result, strconv.Itoa(p))
	}
	return result
}
func riskCategory(label string) string {
	switch label {
	case "代理", "VPN", "Tor", "iCloud 中继", "匿名出口":
		return "匿名"
	case "邮件 DNSBL", "Shodan 标签":
		return "黑名单"
	case "开放端口", "已知漏洞", "自签证书":
		return "暴露面"
	case "机房 IP", "移动网络", "伪家宽", "人类流量 <50%", "广播 IP":
		return "网络"
	default:
		return "滥用"
	}
}
