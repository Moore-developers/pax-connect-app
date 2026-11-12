// Package paxobs provides a passive, bounded-observer seam. It performs no I/O
// and never changes routing, forwarding, or protocol handshake behavior.
package paxobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Event contains value-only runtime evidence. The consumer must not block forwarding.
type Event struct {
	Purpose, IPSource, ProcessStatus, Code, QueryType, DNSResult                         string
	Answers                                                                              []string
	ID, Stage, Source, Inbound, Network, Domain, IP, Process, Outbound, Rule, Raw, Level string
	Chain                                                                                []string
	Port                                                                                 uint16
	At                                                                                   time.Time
	Duration                                                                             time.Duration
	Upload, Download                                                                     int64
}
type sinkKey struct{}
type flowKey struct{}
type flowState struct {
	id                           string
	mu                           sync.Mutex
	dial                         time.Time
	ready                        bool
	responded                    bool
	process, processStatus, rule string
}

// WithSink attaches a process-owned observer to the runtime context.
func WithSink(ctx context.Context, emit func(Event)) context.Context {
	return context.WithValue(ctx, sinkKey{}, emit)
}

// WithConnection establishes one identity inherited by logging and trackers.
func WithConnection(ctx context.Context) context.Context {
	if ctx.Value(sinkKey{}) == nil {
		return ctx
	}
	if _, ok := ctx.Value(flowKey{}).(*flowState); ok {
		return ctx
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ctx
	}
	return context.WithValue(ctx, flowKey{}, &flowState{id: hex.EncodeToString(b[:])})
}

// ID returns the identity only when established at the routing boundary.
func ID(ctx context.Context) string {
	if s, ok := ctx.Value(flowKey{}).(*flowState); ok {
		return s.id
	}
	return ""
}

// Emit synchronously invokes the nonblocking owner callback with a value snapshot.
func Emit(ctx context.Context, e Event) {
	emit, _ := ctx.Value(sinkKey{}).(func(Event))
	if emit == nil {
		return
	}
	e.ID = ID(ctx)
	if e.ID == "" && e.Stage != "diagnostic" {
		return
	}
	if s, ok := ctx.Value(flowKey{}).(*flowState); ok {
		s.mu.Lock()
		if e.Stage == "identity" {
			s.process, s.processStatus = e.Process, e.ProcessStatus
		}
		if e.Stage == "matched" {
			s.rule = e.Rule
		}
		s.mu.Unlock()
	}
	e.At = time.Now()
	emit(e)
}

// DialStarted records monotonic time after route preparation.
func DialStarted(ctx context.Context) {
	if s, ok := ctx.Value(flowKey{}).(*flowState); ok {
		s.mu.Lock()
		s.dial = time.Now()
		s.mu.Unlock()
		Emit(ctx, Event{Stage: "dial_started"})
	}
}

// DialResult reports an observed failure, or a proven transport-ready completion.
func DialResult(ctx context.Context, err error, ready bool) {
	s, ok := ctx.Value(flowKey{}).(*flowState)
	if !ok {
		return
	}
	s.mu.Lock()
	start := s.dial
	if ready && s.ready {
		s.mu.Unlock()
		return
	}
	if ready {
		s.ready = true
	}
	s.mu.Unlock()
	if start.IsZero() {
		return
	}
	if err != nil {
		Emit(ctx, Event{Stage: "failed", Duration: time.Since(start), Raw: err.Error(), Level: "error", Code: FailureCode(err)})
	} else if ready {
		Emit(ctx, Event{Stage: "ready", Duration: time.Since(start)})
	}
}

// ResponseReceived observes the first inbound payload on the existing tracker.
// It proves the channel responded, while duration remains an upper bound.
func ResponseReceived(ctx context.Context, bytes int64) {
	if bytes <= 0 {
		return
	}
	s, ok := ctx.Value(flowKey{}).(*flowState)
	if !ok {
		return
	}
	s.mu.Lock()
	if s.responded || s.dial.IsZero() {
		s.mu.Unlock()
		return
	}
	s.responded = true
	s.ready = true
	start := s.dial
	s.mu.Unlock()
	Emit(ctx, Event{Stage: "response", Duration: time.Since(start), Rule: "first_response"})
}

// Rule names are immutable labels captured when a table is constructed. They
// contain caller-owned display values only, never alter match or outbound logic.
type ruleNamesKey struct{}
type ruleNamesState struct {
	mu     sync.RWMutex
	values []string
}

func WithRuleNames(ctx context.Context) context.Context {
	return context.WithValue(ctx, ruleNamesKey{}, &ruleNamesState{})
}
func SetRuleNames(ctx context.Context, names []string) {
	if state, ok := ctx.Value(ruleNamesKey{}).(*ruleNamesState); ok {
		state.mu.Lock()
		state.values = append([]string(nil), names...)
		state.mu.Unlock()
	}
}
func RuleNames(ctx context.Context) []string {
	if state, ok := ctx.Value(ruleNamesKey{}).(*ruleNamesState); ok {
		state.mu.RLock()
		defer state.mu.RUnlock()
		return append([]string(nil), state.values...)
	}
	return nil
}
