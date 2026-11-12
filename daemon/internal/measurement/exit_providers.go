package measurement

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type rateWindow struct {
	limit  int
	window time.Duration
}
type exitProvider struct {
	id, endpoint       string
	slots, tier, daily int
	utcDay             bool
	windows            []rateWindow
}

var exitProviders = []exitProvider{
	{id: "ipwho.is", endpoint: "https://ipwho.is/", slots: 2, daily: 1000},
	{id: "ip.sb", endpoint: "https://api.ip.sb/geoip", slots: 2, windows: []rateWindow{{5, time.Second}, {100, time.Minute}}},
	{id: "freeipapi", endpoint: "https://free.freeipapi.com/api/v1/json/", slots: 2, windows: []rateWindow{{10, 10 * time.Second}, {60, time.Minute}}},
	{id: "geojs", endpoint: "https://get.geojs.io/v1/ip/geo.json", slots: 1},
	{id: "ipquery", endpoint: "https://api.ipquery.io/?format=json", slots: 1},
	{id: "ipapi.is", endpoint: "https://api.ipapi.is/", slots: 1, tier: 1, daily: 30, utcDay: true},
	{id: "cloudflare", endpoint: "https://www.cloudflare.com/cdn-cgi/trace", slots: 2, tier: 2},
}

func queryGeoProvider(ctx context.Context, client Client, provider, target string, now time.Time) (ExitResult, string, int, time.Duration, bool, string) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return ExitResult{}, "exit_query_failed", 0, 0, false, ""
	}
	request.Header.Set("User-Agent", "PaxConnect/1.0")
	request.Header.Set("Accept", "application/json,text/plain")
	response, err := client.Do(request)
	if err != nil {
		var node *Failure
		nodeFailure := errors.As(err, &node) && (node.Stage == "node_connection" || node.Stage == "node_transport")
		return ExitResult{}, exitFailure(err), 0, 0, nodeFailure, exitFailureReason(err)
	}
	defer func() { _ = response.Body.Close() }()
	retry := retryDuration(response.Header.Get("Retry-After"), now)
	if response.StatusCode != http.StatusOK {
		return ExitResult{}, "exit_http_failed", response.StatusCode, retry, false, strconv.Itoa(response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 16385))
	if err != nil {
		return ExitResult{}, exitFailure(err), response.StatusCode, 0, false, exitFailureReason(err)
	}
	if len(body) > 16384 {
		return ExitResult{}, "exit_response_invalid", response.StatusCode, 0, false, ""
	}
	if status, retry, code := providerBusinessError(provider, body, now); code != "" {
		return ExitResult{}, code, status, retry, false, ""
	}
	result, code := parseGeoProvider(provider, body)
	result.Source = provider
	result.GeoSource = provider
	result.At = time.Now()
	result.GeoAt = result.At
	return result, code, response.StatusCode, retry, false, ""
}
func retryDuration(value string, now time.Time) time.Duration {
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(min(seconds, 86400)) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil && at.After(now) {
		return min(at.Sub(now), 24*time.Hour)
	}
	return 0
}
func parseGeoProvider(provider string, body []byte) (ExitResult, string) {
	var result ExitResult
	if provider == "cloudflare" {
		ip, code := parseExit(body)
		if code != "" {
			return result, code
		}
		result.IP = ip
		for _, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(line, "loc=") {
				result.CountryCode = countryCode(strings.TrimPrefix(line, "loc="))
			}
		}
		return result, ""
	}
	var object map[string]any
	if json.Unmarshal(body, &object) != nil {
		return result, "exit_response_invalid"
	}
	if success, ok := object["success"].(bool); ok && !success {
		return result, "exit_response_invalid"
	}
	if value, ok := object["error"]; ok {
		switch v := value.(type) {
		case bool:
			if v {
				return result, "exit_response_invalid"
			}
		case string:
			if v != "" {
				return result, "exit_response_invalid"
			}
		case nil:
		default:
			return result, "exit_response_invalid"
		}
	}
	if provider == "freeipapi" {
		object["ip"] = object["ipAddress"]
	}
	ip, err := netip.ParseAddr(textField(object, "ip"))
	if err != nil || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return result, "exit_response_invalid"
	}
	result.IP = ip.Unmap().String()
	result.Country = textField(object, "country")
	result.CountryCode = countryCode(textField(object, "country_code"))
	result.Region = textField(object, "region")
	result.City = textField(object, "city")
	result.ISP = textField(object, "isp")
	result.ASN = asnField(object["asn"])
	switch provider {
	case "freeipapi":
		result.Country = textField(object, "countryName")
		result.CountryCode = countryCode(textField(object, "countryCode"))
		result.Region = textField(object, "regionName")
		result.City = textField(object, "cityName")
		result.ISP = textField(object, "asnOrganization")
	case "geojs":
		result.ISP = textField(object, "organization_name")
		if result.ISP == "Unknown" {
			result.ISP = ""
		}
		if result.ASN == "AS64512" || result.ASN == "64512" {
			result.ASN = ""
		}
	case "ipquery":
		if location, ok := object["location"].(map[string]any); ok {
			result.Country = textField(location, "country")
			result.CountryCode = countryCode(textField(location, "country_code"))
			result.Region = textField(location, "state")
			result.City = textField(location, "city")
		}
		if isp, ok := object["isp"].(map[string]any); ok {
			result.ISP = textField(isp, "isp")
			result.ASN = asnField(isp["asn"])
		}
	case "ipwho.is":
		if connection, ok := object["connection"].(map[string]any); ok {
			result.ISP = textField(connection, "isp")
			result.ASN = asnField(connection["asn"])
		}
	case "ipapi.is":
		if location, ok := object["location"].(map[string]any); ok {
			result.Country = textField(location, "country")
			result.CountryCode = countryCode(textField(location, "country_code"))
			result.Region = textField(location, "state")
			result.City = textField(location, "city")
		}
		if company, ok := object["company"].(map[string]any); ok {
			result.ISP = textField(company, "name")
		} else {
			result.ISP = textField(object, "company")
		}
		if asn, ok := object["asn"].(map[string]any); ok {
			result.ASN = asnField(asn["asn"])
		}
	}
	return result, ""
}
func textField(object map[string]any, key string) string {
	value, _ := object[key].(string)
	return boundedText(value)
}
func boundedText(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 128 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return ""
	}
	return value
}
func countryCode(value string) string {
	value = strings.ToUpper(value)
	if len(value) != 2 || value[0] < 'A' || value[0] > 'Z' || value[1] < 'A' || value[1] > 'Z' {
		return ""
	}
	return value
}
func asnField(value any) string {
	switch v := value.(type) {
	case string:
		return boundedText(v)
	case float64:
		if v >= 0 && v <= 4294967295 && v == float64(uint64(v)) {
			return "AS" + strconv.FormatUint(uint64(v), 10)
		}
	}
	return ""
}

func providerBusinessError(provider string, body []byte, now time.Time) (int, time.Duration, string) {
	now = now.UTC()
	var object map[string]any
	if json.Unmarshal(body, &object) != nil {
		return 0, 0, ""
	}
	errorCode := textField(object, "error_code")
	if errorCode == "ERR_FREE_TIER_EXHAUSTED" || errorCode == "ERR_RATE_LIMITED" {
		retry := time.Minute
		if provider == "ipapi.is" {
			retry = time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC).Sub(now)
		}
		return http.StatusTooManyRequests, retry, "exit_rate_limited"
	}
	if errorCode == "ERR_FORBIDDEN_API_KEY_REQUIRED" || errorCode == "ERR_INVALID_API_KEY" {
		return http.StatusForbidden, 0, "exit_provider_auth_required"
	}
	return 0, 0, ""
}
