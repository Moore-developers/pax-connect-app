package measurement

import (
	"context"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ExitResult is bounded public-IP evidence, never raw responses or coordinates.
type ExitResult struct {
	IP, CountryCode, Country, Region, City, ISP, ASN string
	Source, GeoSource, GeoError                      string
	At, GeoAt                                        time.Time
	reason                                           string // sanitized failure evidence; set only on failed requests
}

// ExitQuery freezes a caller's opaque route/network scope and its evidence purpose.
type ExitQuery struct{ Scope, Purpose string }
type geoCache struct {
	value ExitResult
	at    time.Time
}
type providerHealth struct {
	failures, inflight, calls int
	until, started            time.Time
	probing                   bool
	recent                    []time.Time
}

// ExitLookup schedules actual HTTP requests for route and physical clients.
// It never owns their transports, credentials, product operations or native Box.
type ExitLookup struct {
	policyRevision                                         uint64
	providers                                              []exitProvider
	mu                                                     sync.Mutex
	cursor, limit, inflight                                int
	health                                                 map[string]*providerHealth
	cache                                                  map[string]geoCache
	sources                                                map[string]string
	queue                                                  []*exitWaiter
	changed                                                chan struct{}
	state                                                  ExitPoolState
	now                                                    func() time.Time
	queueTimeout, queryTimeout, attemptTimeout, hedgeDelay time.Duration
}

func NewExitLookup() *ExitLookup {
	q := &ExitLookup{providers: append([]exitProvider(nil), exitProviders...), limit: 8, health: map[string]*providerHealth{}, cache: map[string]geoCache{}, sources: map[string]string{}, changed: make(chan struct{}), now: time.Now, queueTimeout: ExitQueueTimeout, queryTimeout: ExitTimeout, attemptTimeout: 5 * time.Second, hedgeDelay: 650 * time.Millisecond}
	for _, p := range q.providers {
		q.health[p.id] = &providerHealth{}
		q.health[p.id+"/unknown"] = &providerHealth{}
		q.health[p.id+"/path/unknown"] = &providerHealth{}
	}
	for _, p := range diagnosticOnlyProviders {
		q.health[p.id] = &providerHealth{}
		q.health[p.id+"/unknown"] = &providerHealth{}
		q.health[p.id+"/path/unknown"] = &providerHealth{}
	}
	return q
}

// Query observes a frozen client. Its first slot wait and network execution have independent deadlines.
func (q *ExitLookup) Query(ctx context.Context, client Client, query ExitQuery) (ExitResult, string) {
	value, code, _ := q.QueryDetail(ctx, client, query)
	return value, code
}

// QueryDetail is Query plus the sanitized failure reason (for example an HTTP status or WS handshake
// status) that belongs to the returned code. It is empty on success and for codes without evidence.
func (q *ExitLookup) QueryDetail(ctx context.Context, client Client, query ExitQuery) (ExitResult, string, string) {
	reasons := map[string]string{}
	q.mu.Lock()
	start := q.cursor % len(q.providers)
	q.cursor++
	q.mu.Unlock()
	task := &exitWaiter{scope: query.Scope, purpose: query.Purpose, start: start, tried: map[string]bool{}}
	waiting, stopWait := context.WithTimeout(ctx, q.queueTimeout)
	defer stopWait()
	var execution context.Context
	var stopExecution context.CancelFunc
	defer func() {
		if stopExecution != nil {
			stopExecution()
		}
	}()
	type outcome struct {
		value    ExitResult
		code     string
		provider exitProvider
		node     bool
	}
	results := make(chan outcome, 2)
	var workers sync.WaitGroup
	var racing context.Context
	var stopRace context.CancelFunc
	defer func() {
		if stopRace != nil {
			stopRace()
		}
		workers.Wait()
	}()
	requests, active := 0, 0
	last := "exit_services_unavailable"
	var winner *ExitResult
	var hedge <-chan time.Time
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	launch := func(lease exitLease) {
		p := lease.provider
		if execution == nil {
			execution, stopExecution = context.WithTimeout(ctx, q.queryTimeout)
			racing, stopRace = context.WithCancel(execution)
		}
		requests++
		active++
		task.tried[p.id] = true
		q.event(ctx, "exit_running", requests)
		workers.Add(1)
		go func() {
			defer workers.Done()
			value, code, node := q.request(racing, client, lease, task.scope, "")
			results <- outcome{value, code, p, node}
		}()
		if timer != nil {
			timer.Stop()
		}
		timer = time.NewTimer(q.hedgeDelay)
		hedge = timer.C
	}
	for {
		if ctx.Err() != nil {
			return ExitResult{}, cancelCode(ctx), reasons[cancelCode(ctx)]
		}
		if active == 0 {
			if requests >= 3 {
				break
			}
			clock := waiting
			if execution != nil {
				clock = execution
			}
			task.retry = requests > 0
			p, code := q.acquire(clock, task, false)
			if code != "" {
				if ctx.Err() != nil {
					return ExitResult{}, cancelCode(ctx), reasons[cancelCode(ctx)]
				}
				if clock.Err() != nil {
					if execution == nil {
						return ExitResult{}, "exit_queue_timeout", reasons["exit_queue_timeout"]
					}
					return ExitResult{}, "exit_timeout", reasons["exit_timeout"]
				}
				if requests > 0 {
					return ExitResult{}, last, reasons[last]
				}
				return ExitResult{}, code, reasons[code]
			}
			launch(p)
		}
		select {
		case <-ctx.Done():
			return ExitResult{}, cancelCode(ctx), reasons[cancelCode(ctx)]
		case <-execution.Done():
			return ExitResult{}, "exit_timeout", reasons["exit_timeout"]
		case <-hedge:
			hedge = nil
			if active == 1 && requests < 3 {
				if p, code := q.acquire(execution, task, true); code == "" {
					launch(p)
				}
			}
		case result := <-results:
			active--
			if result.code != "" && result.value.reason != "" {
				reasons[result.code] = result.value.reason
			}
			if result.node {
				return ExitResult{}, result.code, reasons[result.code]
			}
			if result.code == "" {
				winner = &result.value
				stopRace()
				workers.Wait()
				// Drain losers before optional enrichment; every request already returned its lease.
				for active > 0 {
					<-results
					active--
				}
			} else {
				last = result.code
				if result.provider.id == "cloudflare" && !task.cfAlias && requests < 3 && (result.code == "exit_dns_failed" || strings.HasPrefix(result.code, "target_connection_")) {
					task.cfAlias = true
					task.tried["cloudflare"] = false
				}
			}
		}
		if winner != nil {
			break
		}
	}
	if winner == nil {
		return ExitResult{}, last, reasons[last]
	}
	if ctx.Err() != nil {
		return ExitResult{}, cancelCode(ctx), reasons[cancelCode(ctx)]
	}
	value := *winner
	q.mu.Lock()
	cached, ok := q.cache[value.IP]
	q.mu.Unlock()
	if !completeGeo(value) && ok && completeGeo(cached.value) && q.now().Sub(cached.at) >= 0 && q.now().Sub(cached.at) < 24*time.Hour {
		value = copyGeo(value, cached.value)
	}
	if query.Purpose != "physical_ip" && !completeGeo(value) && requests < 3 && execution.Err() == nil {
		task.tried[value.Source] = true
		task.geo = true
		task.retry = true
		for requests < 3 && execution.Err() == nil {
			p, code := q.acquire(execution, task, false)
			if code != "" {
				break
			}
			requests++
			task.tried[p.provider.id] = true
			q.event(ctx, "exit_running", requests)
			geo, code, node := q.request(execution, client, p, task.scope, value.IP)
			if node {
				break
			}
			if code == "" && geo.IP == value.IP && completeGeo(geo) {
				value = copyGeo(value, geo)
				break
			}
		}
	}
	if ctx.Err() != nil {
		return ExitResult{}, cancelCode(ctx), reasons[cancelCode(ctx)]
	}
	if !completeGeo(value) && query.Purpose != "physical_ip" {
		value.GeoError = "exit_location_unavailable"
	}
	q.cacheGeo(value)
	return value, "", ""
}
func cancelCode(ctx context.Context) string {
	if ctx.Err() == context.Canceled {
		return "operation_cancelled"
	}
	return "exit_timeout"
}
func completeGeo(v ExitResult) bool {
	return (v.Country != "" || v.CountryCode != "") && v.Region != "" && v.City != ""
}
func copyGeo(ip, geo ExitResult) ExitResult {
	ip.CountryCode = geo.CountryCode
	ip.Country = geo.Country
	ip.Region = geo.Region
	ip.City = geo.City
	ip.ISP = geo.ISP
	ip.ASN = geo.ASN
	ip.GeoSource = geo.GeoSource
	ip.GeoAt = geo.GeoAt
	return ip
}
func (q *ExitLookup) cacheGeo(value ExitResult) {
	if value.Country == "" && value.CountryCode == "" {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.cache) >= 512 {
		var oldest string
		var at time.Time
		for ip, entry := range q.cache {
			if oldest == "" || entry.at.Before(at) {
				oldest = ip
				at = entry.at
			}
		}
		delete(q.cache, oldest)
	}
	q.cache[value.IP] = geoCache{value, value.GeoAt}
}
func providerTarget(p exitProvider, ip string) string {
	if ip == "" {
		return p.endpoint
	}
	switch p.id {
	case "ipwho.is":
		return p.endpoint + url.PathEscape(ip)
	case "ip.sb":
		return p.endpoint + "/" + url.PathEscape(ip)
	case "ipapi.is":
		return p.endpoint + "?q=" + url.QueryEscape(ip)
	case "freeipapi":
		return p.endpoint + url.PathEscape(ip)
	case "geojs":
		return "https://get.geojs.io/v1/ip/geo/" + url.PathEscape(ip) + ".json"
	case "ipquery":
		return "https://api.ipquery.io/" + url.PathEscape(ip) + "?format=json"
	}
	return p.endpoint
}
func (q *ExitLookup) request(ctx context.Context, client Client, lease exitLease, scope, ip string) (ExitResult, string, bool) {
	p := lease.provider
	started := time.Now()
	bounded, cancel := context.WithTimeout(ctx, q.attemptTimeout)
	defer cancel()
	target := providerTarget(p, ip)
	if lease.alias {
		target = "https://1.1.1.1/cdn-cgi/trace"
	}
	value, code, status, retry, node, reason := queryGeoProvider(bounded, client, p.id, target, q.now())
	if ip != "" && code == "" && value.IP != ip {
		code = "exit_response_invalid"
	}
	if code != "" {
		value.reason = reason
	}
	observedSource := value
	if ip != "" {
		observedSource.IP = ""
	}
	q.finish(lease, scope, observedSource, code, status, retry, node || ctx.Err() != nil)
	observed := code
	if ctx.Err() == context.Canceled {
		observed = "operation_cancelled"
	}
	observeExit(ctx, ExitAttempt{Provider: p.id, Code: observed, HTTPStatus: status, Duration: time.Since(started), Event: "attempt_complete", Pool: q.Snapshot()})
	return value, code, node
}
func sourceGroup(ip string) string {
	address, err := netip.ParseAddr(ip)
	if err != nil {
		return "unknown"
	}
	address = address.Unmap()
	if address.Is6() {
		return netip.PrefixFrom(address, 64).Masked().String()
	}
	return address.String()
}

// Locate queries a specified public IP through the same bounded provider pool.
// It never treats that IP as the caller's source or falls back to self-query.
func (q *ExitLookup) Locate(ctx context.Context, client Client, ip string, query ExitQuery) (ExitResult, string) {
	address, err := netip.ParseAddr(ip)
	if err != nil || !address.IsGlobalUnicast() || address.IsPrivate() {
		return ExitResult{}, "exit_response_invalid"
	}
	ip = address.Unmap().String()
	q.mu.Lock()
	cached, ok := q.cache[ip]
	start := q.cursor % len(q.providers)
	q.cursor++
	q.mu.Unlock()
	if ok && (cached.value.Country != "" || cached.value.CountryCode != "") && q.now().Sub(cached.at) >= 0 && q.now().Sub(cached.at) < 24*time.Hour {
		return cached.value, ""
	}
	task := &exitWaiter{scope: query.Scope, purpose: query.Purpose, start: start, geo: true, tried: map[string]bool{}}
	waiting, stop := context.WithTimeout(ctx, q.queueTimeout)
	defer stop()
	execution := waiting
	var end context.CancelFunc
	last := "exit_location_unavailable"
	for attempt := 0; attempt < 3; attempt++ {
		task.retry = attempt > 0
		lease, code := q.acquire(execution, task, false)
		if code != "" {
			return ExitResult{}, code
		}
		if end == nil {
			execution, end = context.WithTimeout(ctx, q.queryTimeout)
			defer end()
		}
		task.tried[lease.provider.id] = true
		value, code, node := q.request(execution, client, lease, task.scope, ip)
		if code == "" && (value.Country != "" || value.CountryCode != "") {
			q.cacheGeo(value)
			return value, ""
		}
		if node || ctx.Err() != nil {
			return ExitResult{}, code
		}
		last = code
	}
	if last == "" {
		last = "exit_location_unavailable"
	}
	return ExitResult{}, last
}
