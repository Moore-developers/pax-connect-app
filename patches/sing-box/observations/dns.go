package paxobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/miekg/dns"
)

// BeginDNS gives every intercepted question its own identity, even on a reused
// UDP/TCP session. Answers are bounded values, never candidate connection IPs.
func BeginDNS(parent context.Context, query *dns.Msg, network, inbound, process string) (context.Context, func(*dns.Msg, error)) {
	if parent.Value(sinkKey{}) == nil || len(query.Question) == 0 {
		return parent, func(*dns.Msg, error) {}
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return parent, func(*dns.Msg, error) {}
	}
	s := &flowState{id: hex.EncodeToString(b[:])}
	if p, ok := parent.Value(flowKey{}).(*flowState); ok {
		p.mu.Lock()
		s.process, s.processStatus, s.rule = p.process, p.processStatus, p.rule
		p.mu.Unlock()
	}
	if process != "" {
		s.process, s.processStatus = process, "identified"
	}
	if s.processStatus == "" {
		s.processStatus = "not_observed"
	}
	if s.rule == "" {
		s.rule = `{"Package":"Pax DNS","Method":"builtin","Target":"DNS 接管"}`
	}
	ctx := context.WithValue(parent, flowKey{}, s)
	question := query.Question[0]
	qtype := dns.TypeToString[question.Qtype]
	if qtype == "" {
		qtype = fmt.Sprintf("TYPE%d", question.Qtype)
	}
	Emit(ctx, Event{Stage: "accepted", Purpose: "dns", Inbound: inbound, Network: network,
		Domain: strings.TrimSuffix(question.Name, "."), Process: s.process, ProcessStatus: s.processStatus,
		Rule: s.rule, QueryType: qtype})
	Emit(ctx, Event{Stage: "dns_started"})
	start := time.Now()
	return ctx, func(response *dns.Msg, err error) {
		e := Event{Stage: "dns_response", Duration: time.Since(start)}
		if err != nil {
			e.Code, e.Raw, e.Level = "dns_failed", err.Error(), "error"
		}
		if response != nil {
			e.DNSResult = dns.RcodeToString[response.Rcode]
			if e.DNSResult == "" {
				e.DNSResult = fmt.Sprintf("RCODE%d", response.Rcode)
			}
			if response.Rcode != dns.RcodeSuccess {
				e.Code, e.Level = "dns_server_failure", "error"
				if response.Rcode == dns.RcodeNameError {
					e.Code = "dns_not_found"
				}
			}
			for _, answer := range response.Answer {
				var value string
				switch v := answer.(type) {
				case *dns.A:
					value = v.A.String()
				case *dns.AAAA:
					value = v.AAAA.String()
				case *dns.CNAME:
					value = strings.TrimSuffix(v.Target, ".")
				}
				if value != "" && len(e.Answers) < 16 {
					e.Answers = append(e.Answers, value)
				}
			}
		}
		Emit(ctx, e)
		Emit(ctx, Event{Stage: "closed"})
	}
}
