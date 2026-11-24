package measurement

import (
	"encoding/json"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

var routeComparisonIDs = []string{"db-ip", "ip2location.io", "ipapi.is", "proxycheck.io", "iplocate", "ripestat"}

func isRouteComparison(id string) bool { return slices.Contains(routeComparisonIDs, id) }

func comparisonTarget(provider exitProvider, ip string) string {
	switch provider.id {
	case "ip2location.io":
		return provider.endpoint + "?ip=" + url.QueryEscape(ip)
	case "ripestat":
		return provider.endpoint + "?resource=" + url.QueryEscape(ip)
	case "ipapi.is":
		return providerTarget(provider, ip)
	default:
		return provider.endpoint + url.PathEscape(ip)
	}
}

func parseRouteComparison(id string, body []byte, expected netip.Addr) NetworkDiagnosticReport {
	invalid := NetworkDiagnosticReport{ProviderError: "provider_response_invalid"}
	var object map[string]any
	if json.Unmarshal(body, &object) != nil {
		return invalid
	}
	source := NetworkDiagnosticSource{Name: id, ProviderID: id}
	report := NetworkDiagnosticReport{}
	if id == "ripestat" {
		return parseRIPEstat(object, source, expected)
	}
	ip := textField(object, "ip")
	source.Country = countryCode(textField(object, "country_code"))
	source.City = textField(object, "city")
	switch id {
	case "db-ip":
		ip = textField(object, "ipAddress")
		source.Country = countryCode(textField(object, "countryCode"))
	case "ip2location.io":
		source.City = textField(object, "city_name")
		source.ASN = comparisonASN(object["asn"])
		source.Org = textField(object, "as")
		addComparisonRisk(&report, source, "代理", object["is_proxy"])
	case "ipapi.is":
		source.ASN = comparisonASN(object["asn"])
		source.Org = textField(object, "company")
		if source.Country == "" {
			source.Country = textField(object, "country")
		}
		if location := comparisonObject(object, "location"); location != nil {
			source.Country = countryCode(textField(location, "country_code"))
			source.City = textField(location, "city")
		}
		if asn := comparisonObject(object, "asn"); asn != nil {
			source.ASN = comparisonASN(asn["asn"])
		}
		if company := comparisonObject(object, "company"); company != nil {
			source.Org = textField(company, "name")
		}
	case "proxycheck.io":
		if textField(object, "status") != "ok" && textField(object, "status") != "warning" {
			return invalid
		}
		for key, item := range object {
			if address, err := netip.ParseAddr(key); err == nil && address.Unmap() == expected {
				ip = key
				record, _ := item.(map[string]any)
				network := comparisonObject(record, "network")
				location := comparisonObject(record, "location")
				source.Country = countryCode(textField(location, "country_code"))
				source.City = textField(location, "city_name")
				source.ASN = comparisonASN(network["asn"])
				source.Org = textField(network, "provider")
				source.ProviderDate = textField(record, "last_updated")
				addComparisonRisks(&report, source, comparisonObject(record, "detections"), "", "hosting")
				break
			}
		}
	case "iplocate":
		source.Name = "IPLocate"
		source.ASN = comparisonASN(comparisonObject(object, "asn")["asn"])
		source.Org = textField(comparisonObject(object, "company"), "name")
		if source.Org == "" {
			source.Org = textField(comparisonObject(object, "asn"), "name")
		}
		addComparisonRisks(&report, source, comparisonObject(object, "privacy"), "is_", "hosting")
	}
	if observed, err := netip.ParseAddr(ip); err != nil || observed.Unmap() != expected {
		return NetworkDiagnosticReport{ProviderError: "provider_ip_mismatch"}
	}
	if source.Country != "" || source.City != "" || source.ASN != "" || source.Org != "" {
		report.Sources = []NetworkDiagnosticSource{source}
	} else if len(report.Facts) == 0 {
		return invalid
	}
	return report
}

func parseRIPEstat(object map[string]any, source NetworkDiagnosticSource, expected netip.Addr) NetworkDiagnosticReport {
	invalid := NetworkDiagnosticReport{ProviderError: "provider_response_invalid"}
	if textField(object, "status") != "ok" {
		return invalid
	}
	data := comparisonObject(object, "data")
	prefix, err := netip.ParsePrefix(textField(data, "prefix"))
	if err != nil {
		return invalid
	}
	if !prefix.Contains(expected) {
		return NetworkDiagnosticReport{ProviderError: "provider_ip_mismatch"}
	}
	rawASNs, _ := data["asns"].([]any)
	if len(rawASNs) == 0 || len(rawASNs) > 16 {
		return invalid
	}
	asns := make([]string, 0, len(rawASNs))
	for _, raw := range rawASNs {
		asn := comparisonASN(raw)
		if asn == "" {
			return invalid
		}
		asns = append(asns, asn)
	}
	source.Name = "RIPEstat"
	source.ASN = strings.Join(asns, " / ")
	return NetworkDiagnosticReport{
		Sources: []NetworkDiagnosticSource{source},
		Facts: []NetworkDiagnosticFact{{
			Group: "source_network", Label: "宣告前缀", Value: prefix.Masked().String(),
			Source: source.Name, State: "information", ProviderID: source.ProviderID,
		}},
	}
}

func comparisonObject(object map[string]any, key string) map[string]any {
	value, _ := object[key].(map[string]any)
	return value
}

func comparisonASN(value any) string {
	field, _, _ := strings.Cut(asnField(value), " ")
	n, err := strconv.ParseUint(strings.TrimPrefix(strings.ToUpper(field), "AS"), 10, 32)
	if err != nil || n == 0 {
		return ""
	}
	return "AS" + strconv.FormatUint(n, 10)
}

func addComparisonRisks(report *NetworkDiagnosticReport, source NetworkDiagnosticSource, object map[string]any, prefix, hosting string) {
	addComparisonRisk(report, source, "代理", object[prefix+"proxy"])
	addComparisonRisk(report, source, "VPN", object[prefix+"vpn"])
	addComparisonRisk(report, source, "Tor", object[prefix+"tor"])
	addComparisonRisk(report, source, "机房 IP", object[prefix+hosting])
}

func addComparisonRisk(report *NetworkDiagnosticReport, source NetworkDiagnosticSource, label string, raw any) {
	marked, ok := raw.(bool)
	if !ok {
		return
	}
	state := "clear"
	if marked {
		state = "hit"
	}
	report.Facts = append(report.Facts, NetworkDiagnosticFact{
		Group: "source_risk", Label: label, Value: riskCategory(label),
		Source: source.Name, State: state, ProviderID: source.ProviderID,
	})
}

func setComparisonCapabilities(result *intelligenceResult) {
	id := result.attempt.ID
	report := result.report
	var country, city, asn bool
	if len(report.Sources) > 0 {
		source := report.Sources[0]
		country, city, asn = source.Country != "", source.City != "", source.ASN != ""
	}
	for _, item := range []struct {
		capability string
		present    bool
	}{{"geo.country", country}, {"geo.city", city}, {"geo.asn", asn}, {"bgp.prefix", id == "ripestat"}} {
		if item.present {
			result.attempt.Capabilities = append(result.attempt.Capabilities, item.capability)
		}
	}
	for _, item := range riskCapabilities {
		if slices.ContainsFunc(report.Facts, func(f NetworkDiagnosticFact) bool { return f.Group == "source_risk" && f.Label == item.label }) {
			result.attempt.Capabilities = append(result.attempt.Capabilities, item.capability)
		}
	}
	complete := country && city && asn
	switch id {
	case "db-ip":
		complete = country && city
	case "ripestat":
		complete = asn
	}
	if !complete {
		result.attempt.State = "partial"
		result.attempt.ErrorCode = "provider_fields_missing"
	}
}

func ip2LocationQuotaExceeded(body []byte) bool {
	var payload struct {
		Error struct {
			ErrorCode json.RawMessage `json:"error_code"`
		}
	}
	return json.Unmarshal(body, &payload) == nil && strings.Trim(string(payload.Error.ErrorCode), `"`) == "10000"
}

func nextUTCDay(now time.Time) time.Duration {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC).Sub(now)
}
