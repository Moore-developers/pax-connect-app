package singbox

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"pax-connect/daemon/internal/measurement"
)

const fixedHealthInterval = 15 * time.Second
const fixedHealthTimeout = 5 * time.Second
const fixedHealthConfirmations = 2

type fixedHealthVerdict uint8

const (
	fixedHealthInconclusive fixedHealthVerdict = iota
	fixedHealthPassed
	fixedHealthFailed
)

type fixedHealthCount struct {
	next             time.Time
	failures, passes int
}

type fixedHealthRound struct {
	tag        string
	blocked    bool
	version    uint64
	generation uint64
	cancel     context.CancelFunc
	done       <-chan fixedHealthVerdict
}

// fixedHealth has one lifecycle owner. Only its bounded probe runs outside that
// owner; results cannot commit until poll rechecks the version and candidate.
type fixedHealth struct {
	ctx        context.Context
	stop       context.CancelFunc
	closed     bool
	version    uint64
	generation uint64
	counts     map[string]*fixedHealthCount
	active     *fixedHealthRound

	// Private fixture inputs are frozen before launching a probe. Product callers
	// never supply endpoints, TLS options or an alternative probe.
	targets  []string
	tls      *tls.Config
	interval time.Duration
	probe    func(context.Context, adapter.Outbound) fixedHealthVerdict
}

func newFixedHealth(ctx context.Context) *fixedHealth {
	lifetime, stop := context.WithCancel(ctx)
	return &fixedHealth{ctx: lifetime, stop: stop, counts: map[string]*fixedHealthCount{}}
}

// cancel joins the current round, including both target requests.
func (h *fixedHealth) cancel() {
	if h.closed {
		return
	}
	h.closed = true
	h.stop()
	h.drain()
}

// drain releases the old instance's outbound before its owner closes/reloads it.
// Cancellation precedes the join; each production round has a five-second cap.
func (h *fixedHealth) drain() {
	if h.active != nil {
		h.active.cancel()
		<-h.active.done
		h.active = nil
	}
}

// invalidate preserves no old verdict or consecutive count, even when a caller
// invalidates twice without changing the public runtime version.
func (h *fixedHealth) invalidate(version uint64) {
	h.version = version
	h.generation++
	h.counts = map[string]*fixedHealthCount{}
	if h.active != nil {
		h.active.cancel()
	}
}

// poll is nonblocking. candidates contains only active fixed-route tags, plus
// blocked tags which still need recovery checks. Values are the current gate.
func (h *fixedHealth) poll(now time.Time, version uint64, candidates map[string]bool, outbound func(string) (adapter.Outbound, bool)) (tag string, blocked bool, changed bool) {
	if h.closed || h.ctx.Err() != nil {
		return "", false, false
	}
	if h.version != version {
		h.invalidate(version)
	}
	for tag := range h.counts {
		if _, present := candidates[tag]; !present {
			delete(h.counts, tag)
		}
	}
	if round := h.active; round != nil {
		if gate, present := candidates[round.tag]; !present || gate != round.blocked {
			round.cancel()
		}
		select {
		case verdict := <-round.done:
			round.cancel()
			h.active = nil
			gate, present := candidates[round.tag]
			if present && gate == round.blocked && round.version == version && round.generation == h.generation {
				count := h.counts[round.tag]
				if count == nil {
					count = &fixedHealthCount{}
					h.counts[round.tag] = count
				}
				switch verdict {
				case fixedHealthPassed:
					count.failures = 0
					count.passes++
					if gate && count.passes >= fixedHealthConfirmations {
						count.passes = 0
						return round.tag, false, true
					}
				case fixedHealthFailed:
					count.passes = 0
					count.failures++
					if !gate && count.failures >= fixedHealthConfirmations {
						count.failures = 0
						return round.tag, true, true
					}
				default:
					count.failures, count.passes = 0, 0
				}
			}
		default:
			return "", false, false
		}
	}
	// Pick the oldest due route, then a stable tag, so a busy first route cannot
	// starve other protected routes while only one round is allowed in flight.
	tags := make([]string, 0, len(candidates))
	for tag := range candidates {
		if h.counts[tag] == nil {
			h.counts[tag] = &fixedHealthCount{}
		}
		tags = append(tags, tag)
	}
	sort.Slice(tags, func(i, j int) bool {
		a, b := h.counts[tags[i]].next, h.counts[tags[j]].next
		return a.Before(b) || a.Equal(b) && tags[i] < tags[j]
	})
	for _, tag := range tags {
		count := h.counts[tag]
		if now.Before(count.next) {
			continue
		}
		out, found := outbound(tag)
		if !found {
			continue
		}
		interval := h.interval
		if interval <= 0 {
			interval = fixedHealthInterval
		}
		count.next = now.Add(interval)
		probe := h.probe
		if probe == nil {
			targets, tlsOptions := slices.Clone(h.targets), h.tls
			probe = func(ctx context.Context, out adapter.Outbound) fixedHealthVerdict {
				return probeFixedRoute(ctx, out, targets, tlsOptions)
			}
		}
		ctx, cancel := context.WithTimeout(h.ctx, fixedHealthTimeout)
		done := make(chan fixedHealthVerdict, 1)
		h.active = &fixedHealthRound{tag: tag, blocked: candidates[tag], version: version, generation: h.generation, cancel: cancel, done: done}
		go func() {
			defer cancel()
			done <- probe(ctx, out)
		}()
		break
	}
	return "", false, false
}

func probeFixedRoute(ctx context.Context, out adapter.Outbound, targets []string, tlsOptions *tls.Config) fixedHealthVerdict {
	if len(targets) == 0 {
		targets = []string{"https://www.google.com/generate_204", "https://cp.cloudflare.com/generate_204"}
	}
	return runFixedHealthTargets(ctx, targets, func(ctx context.Context, target string) fixedHealthVerdict {
		return probeFixedTarget(ctx, out, target, tlsOptions)
	})
}

func runFixedHealthTargets(ctx context.Context, targets []string, probe func(context.Context, string) fixedHealthVerdict) fixedHealthVerdict {
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	results := make(chan fixedHealthVerdict, len(targets))
	for _, target := range targets {
		go func() { results <- probe(ctx, target) }()
	}
	passed, failed := false, 0
	for range targets {
		verdict := <-results
		switch verdict {
		case fixedHealthPassed:
			passed = true
			stop() // The sibling is still drained before this round returns.
		case fixedHealthFailed:
			failed++
		}
	}
	if passed {
		return fixedHealthPassed
	}
	if failed == len(targets) {
		return fixedHealthFailed
	}
	return fixedHealthInconclusive
}

func probeFixedTarget(ctx context.Context, out adapter.Outbound, target string, tlsOptions *tls.Config) fixedHealthVerdict {
	transport := measurementHTTPTransport(out)
	// http.Transport detaches its dial context from the request. Bind the actual
	// outbound call to this round, and join it before the instance can be closed.
	dial := transport.DialContext
	var dialMu sync.Mutex
	var dials sync.WaitGroup
	closing := false
	transport.DialContext = func(httpContext context.Context, network, address string) (net.Conn, error) {
		dialMu.Lock()
		if closing {
			dialMu.Unlock()
			return nil, context.Canceled
		}
		dials.Add(1)
		dialMu.Unlock()
		defer dials.Done()
		dialContext, cancel := context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(httpContext, cancel)
		defer stop()
		return dial(dialContext, network, address)
	}
	transport.TLSClientConfig = tlsOptions
	transport.TLSHandshakeTimeout = 3 * time.Second
	transport.DisableKeepAlives = true
	transport.ForceAttemptHTTP2 = false
	transport.MaxConnsPerHost = 1
	defer func() {
		dialMu.Lock()
		closing = true
		dialMu.Unlock()
		transport.CloseIdleConnections()
		dials.Wait()
	}()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fixedHealthInconclusive
	}
	req.Header.Set("User-Agent", "PaxConnect-FixedRouteHealth/1")
	response, err := client.Do(req)
	if response != nil {
		_ = response.Body.Close()
	}
	if err == nil {
		if response.StatusCode == http.StatusNoContent {
			return fixedHealthPassed
		}
		return fixedHealthInconclusive
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return fixedHealthInconclusive
	}
	return fixedHealthFailure(err)
}

func fixedHealthFailure(err error) fixedHealthVerdict {
	failure := measurement.Classify(err, false)
	// A target HTTP/TLS response proves enough of the path to avoid blaming its
	// route. A certificate failure while establishing the node is a node failure.
	if failure.Code == "target_tls_failed" || failure.Code == "proxy_target_rejected" {
		return fixedHealthInconclusive
	}
	if failure.Stage == "target_https" && failure.Code == "target_connection_failed" {
		return fixedHealthInconclusive
	}
	return fixedHealthFailed
}
