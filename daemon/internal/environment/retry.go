package environment

import (
	"context"
	"time"

	"pax-connect/daemon/internal/measurement"
)

// RetrySources refreshes only exact-IP intelligence; browser observations stay frozen.
func (s *Session) RetrySources(ctx context.Context) Snapshot {
	value := s.Read()
	type result struct {
		ip    string
		intel measurement.NetworkDiagnosticReport
		check Check
	}
	ips := map[string]bool{}
	for _, sample := range []Sample{value.Mac, value.Browser} {
		ip := sample.IPv4
		if ip == "" {
			ip = sample.IPv6
		}
		if ip != "" {
			ips[ip] = true
		}
	}
	replies := make(chan result, len(ips))
	for ip := range ips {
		go func(ip string) {
			started := time.Now()
			intel := s.lookup.DiagnoseIP(ctx, s.client, ip, "environment/"+value.ID)
			check := Check{Kind: "intelligence", Target: "ip-intelligence", State: "observed", At: started, DurationMS: uint64(time.Since(started).Milliseconds()), ErrorCode: intel.ProviderError}
			if check.ErrorCode != "" {
				check.State = "failed"
			}
			replies <- result{ip, intel, check}
		}(ip)
	}
	for range ips {
		reply := <-replies
		for _, sample := range []*Sample{&value.Mac, &value.Browser} {
			ip := sample.IPv4
			if ip == "" {
				ip = sample.IPv6
			}
			if ip != reply.ip {
				continue
			}
			// Failed refreshes retain successful same-IP data, but record this attempt.
			if reply.intel.ProviderError == "" || sample.Intelligence.ProviderError != "" || (len(sample.Intelligence.Sources) == 0 && sample.Intelligence.Score == nil && len(sample.Intelligence.Providers) == 0) {
				sample.Intelligence = reply.intel
			}
			checks := make([]Check, 0, len(sample.Checks)+1)
			for _, check := range sample.Checks {
				if check.Kind != "intelligence" {
					checks = append(checks, check)
				}
			}
			sample.Checks = append(checks, reply.check)
		}
	}
	if ctx.Err() != nil {
		return s.Read()
	}
	s.mu.Lock()
	s.state.Mac = value.Mac
	s.state.Browser = value.Browser
	s.mu.Unlock()
	return s.Read()
}
