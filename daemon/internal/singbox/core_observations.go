package singbox

import (
	"context"
	"crypto/rand"
	"fmt"
	"github.com/sagernet/sing-box/experimental/paxobs"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/telemetry"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// byteBucket is the per-flow byte merge window: samples are 5 s wide (history-storage §4.3).
const byteBucket = 5 * time.Second

type observedFlow struct {
	fact     config.FlowFact
	at       time.Time
	up, down uint64
}
type coreObservations struct {
	probes               map[string]*currentPathLease
	probeIDs             map[string]*currentPathLease
	baseline             *activeBaseline
	collecting           atomic.Bool
	mu                   sync.Mutex
	session              string
	epoch                uint64
	sequence, lostBefore uint64
	bytes                int
	records              []config.Observation
	flows                map[string]*observedFlow
	totals               map[string]config.UsageCounter
}

func newCoreObservations() *coreObservations {
	return &coreObservations{probes: map[string]*currentPathLease{}, probeIDs: map[string]*currentPathLease{}, session: rand.Text(), flows: map[string]*observedFlow{}, totals: map[string]config.UsageCounter{}}
}
func (o *coreObservations) append(e config.Observation) {
	o.sequence++
	e.Session = o.session
	e.Sequence = o.sequence
	size := observationSize(e)
	for len(o.records) > 0 && (len(o.records) >= 32768 || o.bytes+size > 8<<20) {
		o.bytes -= observationSize(o.records[0])
		o.lostBefore = o.records[0].Sequence + 1
		o.records = o.records[1:]
	}
	o.records = append(o.records, e)
	o.bytes += size
}

func observationSize(e config.Observation) int {
	size := len(e.Raw) + 1024
	if e.Flow != nil && e.Flow.DNS != nil {
		for _, answer := range e.Flow.DNS.Answers {
			size += len(answer) + 16
		}
	}
	return size
}
func (o *coreObservations) flush(f *observedFlow) {
	if f.up == 0 && f.down == 0 {
		return
	}
	fact := f.fact
	o.append(config.Observation{At: f.at, Kind: "sample", Flow: &fact, Upload: f.up, Download: f.down})
	total := o.totals[f.fact.RouteID]
	total.Tag = f.fact.RouteID
	total.Upload += f.up
	total.Download += f.down
	o.totals[total.Tag] = total
	f.up = 0
	f.down = 0
}
func (o *coreObservations) capture(e paxobs.Event) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.captureProbe(e) || e.Stage == "detour" {
		return
	}
	if e.ID == "" && e.Stage == "diagnostic" {
		if e.Level == "error" || e.Level == "warn" {
			raw, _, _ := telemetry.ScrubRaw(e.Raw)
			o.append(config.Observation{At: e.At, Kind: "diagnostic", Raw: raw, Level: e.Level})
		}
		return
	}
	f := o.flows[e.ID]
	if e.Stage == "accepted" {
		if e.Inbound != "tun" && e.Inbound != "mixed" {
			return
		}
		if len(o.flows) >= 20000 {
			o.sequence++
			o.lostBefore = o.sequence + 1
			return
		}
		key := fmt.Sprintf("%s/%d/%s", o.session, o.epoch, e.ID)
		fact := config.FlowFact{Key: key, StartedAt: e.At, Domain: e.Domain, IP: validObservedIP(e.IP), Port: e.Port, Transport: e.Network, Direction: "unknown", EstablishmentState: "not_measurable", Purpose: "access", Phase: "routing", Process: e.Process, ProcessStatus: e.ProcessStatus}
		if fact.IP != "" {
			fact.IPSource = "destination"
		}
		if e.Purpose == "dns" {
			fact.Purpose, fact.Direction, fact.RouteName = "dns", "dns", "Pax DNS"
			fact.Phase = "dns"
			fact.DNS = &config.DNSFact{QueryType: e.QueryType}
			recordFlowRule(&fact, e.Rule, e.At)
		}
		o.flows[e.ID] = &observedFlow{fact: fact}
		return
	}
	if f == nil {
		return
	}
	if e.Stage == "dns_control" {
		delete(o.flows, e.ID)
		return
	}
	if e.Stage == "identity" {
		f.fact.Process, f.fact.ProcessStatus = e.Process, e.ProcessStatus
		return
	}
	if e.Stage == "bytes" {
		at := e.At.UTC().Truncate(byteBucket).Add(byteBucket)
		if !f.at.IsZero() && !at.Equal(f.at) {
			o.flush(f)
		}
		f.at = at
		f.up += uint64(max(0, e.Upload))
		f.down += uint64(max(0, e.Download))
		return
	}
	if e.Stage == "matched" {
		recordFlowRule(&f.fact, e.Rule, e.At)
		if e.Process != "" {
			f.fact.Process, f.fact.ProcessStatus = e.Process, "identified"
		}
		if e.Network != "" {
			f.fact.Transport = e.Network
		}
		if e.Domain != "" {
			f.fact.Domain = e.Domain
		}
		return
	}
	if e.Stage == "routed" {
		f.fact.Transport = e.Network
		f.fact.Domain = e.Domain
		f.fact.IP = validObservedIP(e.IP)
		f.fact.IPSource = "not_observed"
		if f.fact.IP != "" {
			f.fact.IPSource = "destination"
		} else if e.Outbound != "direct" {
			f.fact.IPSource = "proxy_domain"
		}
		f.fact.Process = e.Process
		if e.Process != "" {
			f.fact.ProcessStatus = "identified"
		}
		f.fact.Port = e.Port
		recordFlowRule(&f.fact, e.Rule, e.At)
		f.fact.RouteID = e.Outbound
		for _, tag := range e.Chain {
			if strings.HasPrefix(tag, "pax-node-") {
				f.fact.RouteID = tag
				break
			}
		}
		f.fact.Direction = "proxy"
		if e.Outbound == "direct" && f.fact.RouteID == "direct" {
			f.fact.Direction = "direct"
		}
		f.fact.RouteName = f.fact.RouteID
		f.fact.Evidence = "native_tracker"
		e.Stage = "started"
	}
	if e.Stage == "dial_started" {
		f.fact.Phase = "connecting"
		e.Stage = "phase"
	}
	if e.Stage == "dns_started" {
		e.Stage = "started"
	}
	if e.Stage == "connected" {
		f.fact.IP, f.fact.IPSource = e.IP, e.IPSource
	}
	if e.Stage == "dns_response" {
		queryType := ""
		if f.fact.DNS != nil {
			queryType = f.fact.DNS.QueryType
		}
		f.fact.DNS = &config.DNSFact{QueryType: queryType, Result: e.DNSResult, Answers: append([]string(nil), e.Answers...)}
		f.fact.ResponseKnown, f.fact.ResponseMS = true, uint64(max(0, e.Duration.Milliseconds()))
		f.fact.EstablishmentState, f.fact.EstablishmentMS, f.fact.Evidence = "response_ready", f.fact.ResponseMS, "dns_response"
		e.Stage = "ready"
		if e.Code != "" {
			e.Stage = "failed"
		}
	}
	if e.Stage == "diagnostic" {
		e.Raw, _, _ = telemetry.ScrubRaw(e.Raw)
	}

	if e.Stage == "ready" {
		f.fact.EstablishmentState = "ready"
		f.fact.EstablishmentMS = uint64(max(0, e.Duration.Milliseconds()))
		if e.Rule == "first_response" {
			f.fact.Evidence = "first_response"
			f.fact.EstablishmentState = "response_ready"
		}
	}
	if e.Stage == "response" {
		f.fact.ResponseKnown = true
		f.fact.ResponseMS = uint64(max(0, e.Duration.Milliseconds()))
		if f.fact.EstablishmentState != "ready" {
			f.fact.EstablishmentState = "response_ready"
			f.fact.EstablishmentMS = f.fact.ResponseMS
			f.fact.Evidence = "first_response"
		}
	}
	if e.Stage == "rejected" {
		if f.fact.RoutingMatchedAt.IsZero() {
			f.fact.RoutingMatchedAt = e.At
		}
		f.fact.Direction = "blocked"
		f.fact.EstablishmentState = "failed"
		f.fact.Failure = "rule_rejected"
	}
	if e.Stage == "failed" {
		f.fact.EstablishmentState = "failed"
		f.fact.Failure = "connection_failed"
		if e.Code != "" {
			f.fact.Failure = e.Code
		}
	}
	if e.Stage == "closed" {
		o.flush(f)
		at := e.At
		f.fact.ClosedAt = &at
	}
	fact := f.fact
	raw := e.Raw

	safe, _, _ := telemetry.ScrubRaw(raw)
	o.append(config.Observation{At: e.At, Kind: e.Stage, Flow: &fact, Raw: safe, Level: e.Level, Code: f.fact.Failure})
	if e.Stage == "closed" {
		delete(o.flows, e.ID)
	}
}
func validObservedIP(ip string) string {
	if ip == "invalid IP" {
		return ""
	}
	return ip
}
func (o *coreObservations) seal() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for id, f := range o.flows {
		o.flush(f)
		at := time.Now()
		f.fact.ClosedAt = &at
		fact := f.fact
		o.append(config.Observation{At: at, Kind: "closed", Flow: &fact})
		delete(o.flows, id)
	}
}

// ReadObservations reads replayable Pax facts without taking the lifecycle owner lock.
func (r *CoreRuntime) ReadObservations(ctx context.Context, session string, after uint64, limit int) config.ObservationBatch {
	o := r.observations
	o.mu.Lock()
	defer o.mu.Unlock()
	now := time.Now().UTC().Truncate(time.Second)
	for _, f := range o.flows {
		if !f.at.After(now) {
			o.flush(f)
		}
	}
	b := config.ObservationBatch{Session: o.session, HighWater: o.sequence, LostBefore: o.lostBefore, Collecting: o.collecting.Load()}
	if session != "" && session != o.session || after > o.sequence {
		b.ResetRequired = true
		after = 0
	}
	if after+1 < o.lostBefore {
		b.ResetRequired = true
	}
	if limit <= 0 || limit > 256 {
		limit = 256
	}
	bytes := 0
	for _, e := range o.records {
		if e.Sequence <= after {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		bytes += 1024 + len(e.Raw)
		if bytes > 1<<20 || len(b.Records) >= limit {
			break
		}
		b.Records = append(b.Records, e)
	}
	return b
}

func recordFlowRule(f *config.FlowFact, raw string, at time.Time) {
	previous := f.Rule + "\x00" + f.RuleSource
	defer func() {
		if raw != "" && (f.RoutingMatchedAt.IsZero() || previous != f.Rule+"\x00"+f.RuleSource) {
			f.RoutingMatchedAt = at
		}
	}()
	if evidence, ok := config.RecordedRoutingEvidence(raw); ok {
		f.Rule = evidence.Method + "=" + evidence.Target
		f.RuleSource = "Pax 包：" + evidence.Package
		return
	}
	f.Rule = raw
	f.RuleSource = "内核规则"
}
