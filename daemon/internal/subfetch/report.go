package subfetch

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"sync"
	"time"
)

// Report contains request timings and controlled outcomes, never source URLs.
type Report struct {
	At                                      time.Time
	Phase                                   string
	Status                                  int
	DNS, Connect, TLS, Headers, Body, Total time.Duration
	Code                                    string
}

// FetchSubscriptionRequest reuses checked DNS within this request only. Redirects
// still pass the same public-address policy; original host names retain TLS identity.
func (f *Fetcher) FetchSubscriptionRequest(ctx context.Context, raw, agent string) ([]byte, Report, error) {
	started := time.Now()
	report := Report{At: started, Phase: "dns"}
	var mu sync.Mutex
	cache := map[string][]netip.Addr{}
	options := f.options
	options.Lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
		mu.Lock()
		cached, ok := cache[host]
		mu.Unlock()
		if ok {
			return append([]netip.Addr(nil), cached...), nil
		}
		at := time.Now()
		values, err := f.lookup(ctx, host)
		mu.Lock()
		report.DNS += time.Since(at)
		if err == nil {
			cache[host] = append([]netip.Addr(nil), values...)
		}
		mu.Unlock()
		return values, err
	}
	session := NewWithOptions(options)
	if transport, ok := session.client.Transport.(*http.Transport); ok {
		defer transport.CloseIdleConnections()
	}
	var tlsAt, headersAt time.Time
	connectStarts := map[string]time.Time{}
	trace := &httptrace.ClientTrace{
		ConnectStart: func(network, address string) {
			mu.Lock()
			report.Phase = "connect"
			connectStarts[network+address] = time.Now()
			mu.Unlock()
		},
		ConnectDone: func(network, address string, _ error) {
			mu.Lock()
			if started, ok := connectStarts[network+address]; ok {
				report.Connect += time.Since(started)
				delete(connectStarts, network+address)
			}
			mu.Unlock()
		},
		TLSHandshakeStart: func() { mu.Lock(); report.Phase = "tls"; tlsAt = time.Now(); mu.Unlock() },
		TLSHandshakeDone: func(_ tls.ConnectionState, _ error) {
			mu.Lock()
			if !tlsAt.IsZero() {
				report.TLS += time.Since(tlsAt)
			}
			mu.Unlock()
		},
		WroteRequest: func(httptrace.WroteRequestInfo) {
			mu.Lock()
			report.Phase = "headers"
			headersAt = time.Now()
			mu.Unlock()
		},
		GotFirstResponseByte: func() {
			mu.Lock()
			if !headersAt.IsZero() {
				report.Headers += time.Since(headersAt)
			}
			mu.Unlock()
		},
	}
	body, err := session.fetch(httptrace.WithClientTrace(ctx, trace), raw, agent, func(phase string, status int, duration time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		report.Phase = phase
		if status != 0 {
			report.Status = status
		}
		report.Body += duration
	})
	mu.Lock()
	defer mu.Unlock()
	report.Total = time.Since(started)
	if err != nil {
		report.Code = err.Error()
	} else {
		report.Phase = "complete"
	}
	return body, report, err
}

// dialAddresses bounds each address and joins losing dials. Checking the full
// resolved set remains the caller's responsibility; no unchecked re-resolution.
func dialAddresses(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error), network, port string, addresses []netip.Addr) (net.Conn, error) {
	if len(addresses) > 16 {
		addresses = addresses[:16]
	}
	ordered := append([]netip.Addr(nil), addresses...)
	for i := 1; i < len(ordered); i++ {
		if ordered[i].Is4() != ordered[0].Is4() {
			ordered[1], ordered[i] = ordered[i], ordered[1]
			break
		}
	}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		conn net.Conn
		err  error
	}
	jobs := make(chan netip.Addr, len(ordered))
	results := make(chan result, len(ordered))
	for _, ip := range ordered {
		jobs <- ip
	}
	close(jobs)
	workers := min(2, len(ordered))
	for i := 0; i < workers; i++ {
		go func() {
			for ip := range jobs {
				bounded, stop := context.WithTimeout(child, 3*time.Second)
				connection, err := dial(bounded, network, net.JoinHostPort(ip.String(), port))
				stop()
				results <- result{connection, err}
			}
		}()
	}
	var chosen net.Conn
	var last error
	for range ordered {
		r := <-results
		if r.err == nil && r.conn != nil {
			if chosen == nil {
				chosen = r.conn
				cancel()
			} else {
				_ = r.conn.Close()
			}
		} else {
			last = r.err
		}
	}
	if chosen != nil {
		return chosen, nil
	}
	if last == nil {
		last = ctx.Err()
	}
	return nil, last
}
