// Package environment samples the current host and a real browser. It owns only
// bounded read-only probes and a token-scoped loopback page, never network policy.
package environment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"pax-connect/daemon/internal/measurement"
)

// Resolver is a resolver actually observed by the DNS test authority.
type Resolver struct {
	IP           string `json:"ip"`
	Country      string `json:"country"`
	Organization string `json:"organization"`
}

// Sample is one independently sampled execution environment.
type Sample struct {
	IPv4         string                              `json:"ipv4"`
	IPv6         string                              `json:"ipv6"`
	Complete     bool                                `json:"complete"`
	Resolvers    []Resolver                          `json:"resolvers"`
	DNSError     string                              `json:"dnsError"`
	Languages    []string                            `json:"languages"`
	Region       string                              `json:"region"`
	Timezone     string                              `json:"timezone"`
	Browser      string                              `json:"browser"`
	RTCAddresses []string                            `json:"rtcAddresses"`
	RTCState     string                              `json:"rtcState"`
	IPError      string                              `json:"ipError"`
	Checks       []Check                             `json:"checks"`
	Intelligence measurement.NetworkDiagnosticReport `json:"-"`
}

func publicIP(value string) string {
	ip, err := netip.ParseAddr(value)
	if err != nil || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return ""
	}
	return ip.Unmap().String()
}

func readBody(ctx context.Context, client measurement.Client, url string) ([]byte, error) {
	bounded, cancel := context.WithTimeout(ctx, 7*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(bounded, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "PaxConnect/1.0")
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("probe_http_" + strconv.Itoa(response.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if len(data) > 65536 {
		return nil, errors.New("probe_response_too_large")
	}
	return data, err
}

var countryPattern = regexp.MustCompile(`^[a-zA-Z]{2}$`)
var dnsIDPattern = regexp.MustCompile(`^[a-zA-Z0-9]{8,64}$`)

func dnsID(ctx context.Context, client measurement.Client) (string, error) {
	body, err := readBody(ctx, client, "https://bash.ws/id")
	id := strings.TrimSpace(string(body))
	if err != nil {
		return "", err
	}
	if !dnsIDPattern.MatchString(id) {
		return "", errors.New("dns_session_invalid")
	}
	return id, nil
}
func dnsResults(ctx context.Context, client measurement.Client, id string) ([]Resolver, error) {
	data, err := readBody(ctx, client, "https://bash.ws/dnsleak/test/"+id+"?json=2")
	if err != nil {
		return nil, err
	}
	return parseResolvers(data)
}
func parseResolvers(data []byte) ([]Resolver, error) {
	var rows []struct{ IP, Country, ASN, Org, Type string }
	if json.Unmarshal(data, &rows) != nil || len(rows) > 128 {
		return nil, errors.New("dns_response_invalid")
	}
	result := []Resolver{}
	seen := map[string]bool{}
	for _, row := range rows {
		ip := publicIP(row.IP)
		if row.Type != "dns" || ip == "" || seen[ip] {
			continue
		}
		if !countryPattern.MatchString(row.Country) || len(row.Org) > 180 || len(row.ASN) > 180 {
			return nil, errors.New("dns_response_invalid")
		}
		seen[ip] = true
		organization := row.Org
		if organization == "" {
			organization = row.ASN
		}
		result = append(result, Resolver{ip, strings.ToUpper(row.Country), organization})
	}
	if len(result) == 0 {
		return nil, errors.New("dns_no_results")
	}
	return result, nil
}
func macSample(ctx context.Context, client measurement.Client) Sample {
	var sample Sample
	var v4, v6, route Check
	var dnsChecks []Check
	var workers sync.WaitGroup
	workers.Add(4)
	go func() { defer workers.Done(); sample.IPv4, v4 = ipCheck(ctx, client, false) }()
	go func() { defer workers.Done(); sample.IPv6, v6 = ipCheck(ctx, client, true) }()
	go func() { defer workers.Done(); route = ipv6RouteCheck(ctx) }()
	go func() {
		defer workers.Done()
		session := Check{Kind: "dns_session", Target: "bash.ws", At: time.Now(), State: "failed"}
		started := time.Now()
		id, err := dnsID(ctx, client)
		session.DurationMS = uint64(time.Since(started).Milliseconds())
		session.ErrorCode = probeError(err)
		if err != nil {
			sample.DNSError = session.ErrorCode
			dnsChecks = append(dnsChecks, session)
			return
		}
		session.State = "observed"
		dnsChecks = append(dnsChecks, session)
		var queries sync.WaitGroup
		query := Check{Kind: "dns_query", Target: "bash.ws", At: time.Now(), State: "attempted", Value: "12 randomized names"}
		started = time.Now()
		for i := 0; i < 12; i++ {
			queries.Add(1)
			go func() {
				defer queries.Done()
				bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
				defer cancel()
				_, _ = net.DefaultResolver.LookupIPAddr(bounded, randomToken()+"."+id+".bash.ws")
			}()
		}
		queries.Wait()
		query.DurationMS = uint64(time.Since(started).Milliseconds())
		dnsChecks = append(dnsChecks, query)
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			sample.DNSError = probeError(ctx.Err())
			return
		case <-timer.C:
		}
		var result Check
		sample.Resolvers, result = dnsResultCheck(ctx, client, id)
		sample.DNSError = result.ErrorCode
		dnsChecks = append(dnsChecks, result)
	}()
	workers.Wait()
	sample.Checks = append([]Check{v4, v6, route}, dnsChecks...)
	if sample.IPv4 == "" && sample.IPv6 == "" {
		sample.IPError = v4.ErrorCode
	}
	sample.Complete = true
	return sample
}

func (s *Session) enrich(ctx context.Context, sample *Sample) {
	ip := sample.IPv4
	if ip == "" {
		ip = sample.IPv6
	}
	if ip == "" {
		if sample.IPError == "" {
			sample.IPError = "ip_unavailable"
		}
		return
	}
	started := time.Now()
	sample.Intelligence = s.intelligence(ctx, ip)
	check := Check{Kind: "intelligence", Target: "ip-intelligence", State: "observed", At: started, DurationMS: uint64(time.Since(started).Milliseconds()), ErrorCode: sample.Intelligence.ProviderError}
	if check.ErrorCode != "" {
		check.State = "failed"
	}
	sample.Checks = append(append([]Check(nil), sample.Checks...), check)
}
func randomToken() string {
	value := make([]byte, 24)
	if _, err := rand.Read(value); err != nil {
		panic("crypto/rand unavailable")
	}
	return hex.EncodeToString(value)
}
