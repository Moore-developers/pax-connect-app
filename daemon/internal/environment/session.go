package environment

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"pax-connect/daemon/internal/measurement"
)

//go:embed browser.html
var browserHTML string

var browserPage = template.Must(template.New("browser").Parse(browserHTML))

//go:embed browser.js
var browserScript string

//go:embed browser_claude.js
var browserClaudeScript string

// Snapshot is an immutable copy of the two independent samples.
type Snapshot struct {
	ID, BrowserURL string
	At             time.Time
	Mac, Browser   Sample
	Complete       bool
	Cancelled      bool
}

// Session owns its probes, listener and 45-second lifetime. Call Close on cancel.
type Session struct {
	mu                sync.Mutex
	state             Snapshot
	cancel            context.CancelFunc
	done              chan struct{}
	client            measurement.Client
	lookup            *measurement.ExitLookup
	intelligenceTasks map[string]*intelligenceTask
	macIPv6Route      string
	browserDNS        string
	browserDNSNames   []string
	browserDNSStarted time.Time
	browserSubmitted  bool
	origin, path      string
	provider          EvidenceProvider
	presentation      *Presentation
}

// Start serves a single-use browser page exclusively on IPv4 loopback.
func Start(parent context.Context, lookup *measurement.ExitLookup, provider EvidenceProvider) (*Session, error) {
	return StartWithPresentation(parent, lookup, provider, nil)
}

func StartWithPresentation(parent context.Context, lookup *measurement.ExitLookup, provider EvidenceProvider, presentation *Presentation) (*Session, error) {
	if err := ValidatePresentation(presentation); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	transport := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 7 * time.Second}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	s := &Session{cancel: cancel, done: make(chan struct{}), client: client, lookup: lookup, provider: provider, presentation: copyPresentation(presentation), origin: "http://" + listener.Addr().String(), path: "/" + randomToken() + "/"}
	s.state = Snapshot{ID: randomToken(), BrowserURL: s.origin + s.path, At: time.Now()}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.serve(ctx, w, r) }), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 3 * time.Second, MaxHeaderBytes: 8192}
	go func() { _ = server.Serve(listener) }()
	go func() {
		<-ctx.Done()
		_ = server.Close()
		transport.CloseIdleConnections()
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, sample := range []*Sample{&s.state.Mac, &s.state.Browser} {
			if !sample.Complete {
				sample.Complete = true
				if sample.IPv4 == "" && sample.IPv6 == "" {
					sample.IPError = "sampling_timeout"
					if s.state.Cancelled {
						sample.IPError = "operation_cancelled"
					}
				}
				if len(sample.Resolvers) == 0 {
					sample.DNSError = "sampling_timeout"
					if s.state.Cancelled {
						sample.DNSError = "operation_cancelled"
					}
				}
				if sample.RTCState == "" {
					sample.RTCState = "failed"
				}
			}
		}
		s.state.Complete = true
		s.presentation = nil
		close(s.done)
	}()
	go func() {
		sample := macSample(ctx, client, provider, func(route Check) {
			s.mu.Lock()
			s.macIPv6Route = route.State
			s.mu.Unlock()
		})
		sample.Complete = false
		s.mu.Lock()
		if ctx.Err() == nil {
			s.state.Mac = sample
		}
		s.mu.Unlock()
		s.enrich(ctx, &sample)
		sample.Complete = true
		s.mu.Lock()
		if ctx.Err() == nil {
			s.state.Mac = sample
		}
		done := s.state.Mac.Complete && s.state.Browser.Complete
		s.state.Complete = done
		s.mu.Unlock()
		if done {
			cancel()
		}
	}()
	return s, nil
}

// Read copies slices so RPC readers cannot mutate the session owner.
func (s *Session) Read() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state // Published samples and their slices are immutable after assignment.
}

// Close cancels all requests and closes the loopback listener.
func (s *Session) Close() {
	s.mu.Lock()
	if !s.state.Complete {
		s.state.Cancelled = true
	}
	s.mu.Unlock()
	s.cancel()
}

// Done closes after the final report has been published and listener closed.
func (s *Session) Done() <-chan struct{} { return s.done }
func (s *Session) serve(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if r.Host != strings.TrimPrefix(s.origin, "http://") || !strings.HasPrefix(r.URL.Path, s.path) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+s.state.ID+"'; style-src 'unsafe-inline'; connect-src 'self' https://ipv4.icanhazip.com https://ipv6.icanhazip.com https://*.bash.ws https://www.cloudflare.com https://ipinfo.io https://myip.ipip.net https://claude.ai https://api.anthropic.com https://status.claude.com; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	endpoint := strings.TrimPrefix(r.URL.Path, s.path)
	if endpoint == "presentation" && r.Method == http.MethodGet {
		s.mu.Lock()
		value := s.presentation
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(value)
		return
	}

	if endpoint == "browser.js" && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = io.WriteString(w, "const paxBrowserCatalog = ")
		_ = json.NewEncoder(w).Encode(browserCatalog())
		_, _ = io.WriteString(w, ";\n"+browserClaudeScript+"\n"+browserScript)
		return
	}
	if endpoint == "" && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		s.mu.Lock()
		value := s.presentation
		s.mu.Unlock()
		pinned := value != nil
		if !pinned {
			value = &Presentation{Locale: "en", Messages: browserCatalog()["en"]}
		}
		_ = browserPage.Execute(w, struct {
			*Presentation
			Pinned bool
			Nonce  string
		}{value, pinned, s.state.ID})
		return
	}
	if r.Header.Get("Origin") != s.origin {
		http.Error(w, "origin rejected", http.StatusForbidden)
		return
	}
	switch {
	case endpoint == "dns/start" && r.Method == http.MethodPost:
		id, err := dnsID(ctx, s.client)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]string{"errorCode": probeError(err)})
			return
		}
		s.mu.Lock()
		s.browserDNS = id
		s.browserDNSNames = dnsProbeNames(id)
		s.browserDNSStarted = time.Now()
		names := append([]string(nil), s.browserDNSNames...)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "names": names})
	case endpoint == "result" && r.Method == http.MethodPost:
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "JSON required", http.StatusUnsupportedMediaType)
			return
		}
		var sample Sample
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&sample); err != nil {
			http.Error(w, "invalid sample", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF || validateBrowser(sample) != nil {
			http.Error(w, "invalid sample", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		if s.browserSubmitted || ctx.Err() != nil {
			s.mu.Unlock()
			http.Error(w, "expired sample", http.StatusConflict)
			return
		}
		s.browserSubmitted = true
		id := s.browserDNS
		names := append([]string(nil), s.browserDNSNames...)
		dnsStarted := s.browserDNSStarted
		started := s.state.At
		s.mu.Unlock()
		returned := time.Now()
		sample.Complete = false
		sample.IPError = ""
		sample.IPv4 = publicIP(sample.IPv4)
		sample.IPv6 = publicIP(sample.IPv6)
		sample.Resolvers = nil
		sample.DNSError = "dns_unavailable"
		if id != "" {
			resolvers, check := dnsResultCheck(ctx, s.client, id)
			sample.Resolvers = resolvers
			sample.DNSError = check.ErrorCode
			sample.Checks = append(sample.Checks, check)
		} else {
			sample.DNSError = "dns_session_unavailable"
			for _, check := range sample.Checks {
				if check.Kind == "dns_session" && check.ErrorCode != "" {
					sample.DNSError = check.ErrorCode
				}
			}
		}
		s.mu.Lock()
		if ctx.Err() == nil {
			s.state.Browser = sample
		}
		s.mu.Unlock()
		// Acknowledge browser and resolver evidence before enriching IP data or waiting for route attribution.
		s.mu.Lock()
		route := s.macIPv6Route
		s.mu.Unlock()
		if route == "" {
			route = "unknown"
		}
		w.Header().Set("X-Pax-IPv6-Route", route)
		dnsState := "failed"
		switch sample.DNSError {
		case "":
			dnsState = "observed"
		case "probe_timeout", "target_connection_timeout":
			dnsState = "timeout"
		}
		w.Header().Set("X-Pax-DNS-Result", dnsState)
		w.WriteHeader(http.StatusNoContent)
		_ = http.NewResponseController(w).Flush()
		s.enrich(ctx, &sample)
		s.mu.Lock()
		if ctx.Err() == nil {
			s.state.Browser = sample
		}
		s.mu.Unlock()
		var routeChecks, dnsChecks []Check
		var evidence sync.WaitGroup
		evidence.Add(1)
		go func() {
			defer evidence.Done()
			routeChecks = s.collectRouteEvidence(ctx, s.provider, started, returned)
		}()
		if len(names) > 0 {
			evidence.Add(1)
			go func() {
				defer evidence.Done()
				dnsChecks = collectDNSPath(ctx, s.provider, names, dnsStarted, returned)
			}()
		}
		evidence.Wait()
		sample.Checks = append(append(append([]Check(nil), sample.Checks...), routeChecks...), dnsChecks...)

		sample.Complete = true
		s.mu.Lock()
		if ctx.Err() == nil {
			s.state.Browser = sample
		}
		done := s.state.Mac.Complete && s.state.Browser.Complete
		s.state.Complete = done
		s.mu.Unlock()
		if done {
			s.cancel()
		}
	default:
		http.NotFound(w, r)
	}
}
func validateBrowser(s Sample) error {
	if err := validateChecks(s.Checks); err != nil {
		return err
	}
	if len(s.Languages) > 16 || len(s.RTCAddresses) > 32 || len(s.Browser) > 80 || len(s.Region) > 80 || len(s.Timezone) > 80 || len(s.IPv4) > 64 || len(s.IPv6) > 64 {
		return errors.New("sample_limit")
	}
	for _, value := range append(append([]string{s.Region, s.Timezone, s.Browser}, s.Languages...), s.RTCAddresses...) {
		if len(value) > 180 || strings.ContainsAny(value, "\x00\r\n") {
			return errors.New("sample_text_invalid")
		}
	}
	if s.Timezone != "" {
		if _, err := time.LoadLocation(s.Timezone); err != nil {
			return err
		}
	}
	for _, value := range s.RTCAddresses {
		if net.ParseIP(value) == nil && !strings.HasSuffix(value, ".local") {
			return errors.New("rtc_address_invalid")
		}
	}
	switch s.RTCState {
	case "complete", "empty", "unsupported", "failed":
	default:
		return errors.New("rtc_state_invalid")
	}
	return nil
}
