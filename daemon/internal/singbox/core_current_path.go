package singbox

import (
	"context"
	"crypto/tls"
	"errors"
	"github.com/sagernet/sing-box/experimental/paxobs"
	"golang.org/x/sys/unix"
	"io"
	"net"
	"net/http"
	"net/url"
	"pax-connect/daemon/internal/config"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"time"
)

const currentPathTarget = "https://www.google.com/generate_204"

type currentPathLease struct {
	ctx            context.Context
	cancel         context.CancelFunc
	request        config.CurrentPathRequest
	version        uint64
	session, mixed string
	mu             sync.Mutex
	source, actual string
	chain          []string
	expected, hops []string
}

// PrepareCurrentPath runs only under the root lifecycle owner. Its returned
// closure uses fixed immutable endpoints and runs without that owner's mutex.
func (r *CoreRuntime) PrepareCurrentPath(ctx context.Context, q config.CurrentPathRequest) (func(context.Context) config.CurrentPathResult, error) {
	if r.Status() != "STARTED" || q.ExpectedTag == "" || r.SelectedTag() != q.ExpectedTag {
		return nil, errors.New("runtime_selection_mismatch")
	}
	o := r.observations
	if q.ExpectedSession != "" && q.ExpectedSession != o.session || q.ExpectedVersion != 0 && q.ExpectedVersion != r.pathVersion.Load() {
		return nil, errors.New("current_path_stale")
	}
	r.cancelCurrentPath(false)
	leaseCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	lease := &currentPathLease{ctx: leaseCtx, cancel: cancel, request: q, version: r.pathVersion.Load(), session: o.session, mixed: r.mixedAddress}
	if lease.mixed == "" {
		cancel()
		return nil, errors.New("mixed_listener_unreachable")
	}
	for tag := q.ExpectedTag; tag != ""; tag = r.pathDetours[tag] {
		if slices.Contains(lease.expected, tag) {
			cancel()
			return nil, errors.New("runtime_selection_mismatch")
		}
		lease.expected = append(lease.expected, tag)
	}
	r.pathMu.Lock()
	r.pathLease = lease
	r.pathMu.Unlock()
	return func(parent context.Context) config.CurrentPathResult {
		runCtx, done := context.WithCancel(lease.ctx)
		defer done()
		defer lease.cancel()
		stop := context.AfterFunc(parent, done)
		defer stop()
		target := r.pathTarget
		tlsOptions := r.pathTLS // private test injection; no product input
		if target == "" {
			target = currentPathTarget
		}
		result := r.runCurrentPath(runCtx, lease, target, tlsOptions)
		r.pathMu.Lock()
		if r.pathLease == lease {
			r.pathLease = nil
		}
		r.pathMu.Unlock()
		if r.pathVersion.Load() != lease.version {
			result.State = "stale"
			result.Cause = "runtime_changed"
		}
		return result
	}, nil
}
func (r *CoreRuntime) cancelCurrentPath(changed bool) {
	if changed {
		r.pathVersion.Add(1)
	}
	r.pathMu.Lock()
	lease := r.pathLease
	r.pathLease = nil
	r.pathMu.Unlock()
	if lease != nil {
		lease.cancel()
	}
}
func (r *CoreRuntime) runCurrentPath(ctx context.Context, lease *currentPathLease, target string, tlsOptions *tls.Config) config.CurrentPathResult {
	result := config.CurrentPathResult{CheckID: lease.request.CheckID, State: "inconclusive", Cause: "target_unconfirmed", CoreSession: lease.session, RuntimeVersion: lease.version, StartedAt: time.Now()}
	defer func() {
		lease.mu.Lock()
		source := lease.source
		lease.mu.Unlock()
		r.observations.mu.Lock()
		delete(r.observations.probes, source)
		r.observations.mu.Unlock()
	}()
	proxyURL, _ := url.Parse("http://" + lease.mixed)
	dialer := &net.Dialer{Timeout: 3 * time.Second, Control: func(_, address string, raw syscall.RawConn) error {
		if address != lease.mixed {
			return errors.New("probe_endpoint_invalid")
		}
		var failure error
		if err := raw.Control(func(fd uintptr) {
			failure = unix.Bind(int(fd), &unix.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}})
			if failure != nil {
				return
			}
			bound, err := unix.Getsockname(int(fd))
			if err != nil {
				failure = err
				return
			}
			local, ok := bound.(*unix.SockaddrInet4)
			if !ok {
				failure = errors.New("probe_source_invalid")
				return
			}
			source := net.JoinHostPort("127.0.0.1", strconv.Itoa(local.Port))
			lease.mu.Lock()
			lease.source = source
			lease.mu.Unlock()
			o := r.observations
			o.mu.Lock()
			o.probes[source] = lease
			o.mu.Unlock()
		}); err != nil {
			return err
		}
		return failure
	}}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != lease.mixed {
			return nil, errors.New("probe_endpoint_invalid")
		}
		return dialer.DialContext(ctx, "tcp4", address)
	}, TLSClientConfig: tlsOptions, DisableKeepAlives: true, ForceAttemptHTTP2: false, MaxConnsPerHost: 1}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err == nil {
		request.Header.Set("User-Agent", "PaxConnect-CurrentPath/1")
		var response *http.Response
		response, err = client.Do(request)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
			_ = response.Body.Close()
			if response.StatusCode == 204 {
				result.State = "passed"
				result.Cause = ""
			} else {
				result.Cause = "target_http_unexpected"
			}
		}
	}
	lease.mu.Lock()
	result.ActualTag = lease.actual
	matched := false
	for _, tag := range lease.chain {
		matched = matched || tag == lease.request.ExpectedTag
	}
	logicalMatched := matched
	for _, tag := range lease.expected[1:] {
		matched = matched && slices.Contains(lease.hops, tag)
	}
	source := lease.source
	lease.mu.Unlock()

	if err != nil && ctx.Err() != nil {
		result.Cause = "check_timeout"
	}

	if !matched {
		result.State = "inconclusive"
		result.Cause = "egress_unconfirmed"
		if result.ActualTag != "" && result.ActualTag != lease.request.ExpectedTag && !logicalMatched {
			result.State = "failed"
			result.Cause = "runtime_selection_mismatch"
		}
	}
	if source == "" || errors.Is(err, syscall.ECONNREFUSED) && result.ActualTag == "" {
		result.State = "failed"
		result.Cause = "mixed_listener_unreachable"
	}
	if ctx.Err() != nil && r.pathVersion.Load() != lease.version {
		result.State = "stale"
		result.Cause = "runtime_changed"
	}
	result.CompletedAt = time.Now()
	return result
}

func (o *coreObservations) captureProbe(e paxobs.Event) bool {
	lease := o.probeIDs[e.ID]
	if e.Stage == "accepted" && e.Inbound == "mixed" {
		if pending := o.probes[e.Source]; pending != nil {
			lease = pending
			o.probeIDs[e.ID] = lease
		}
	}
	if lease == nil {
		return false
	}
	lease.mu.Lock()
	if e.Stage == "routed" {
		lease.actual = e.Outbound
		lease.chain = append([]string(nil), e.Chain...)
	}
	if e.Stage == "detour" {
		lease.hops = append(lease.hops, e.Outbound)
	}
	lease.mu.Unlock()
	if e.Stage == "closed" {
		delete(o.probeIDs, e.ID)
	}
	return true
}

// CurrentPathIdentity is captured by the lifecycle owner, never by a network worker.
func (r *CoreRuntime) CurrentPathIdentity() (string, uint64) {
	return r.observations.session, r.pathVersion.Load()
}
