package controller

import (
	"context"
	"errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "pax-connect/daemon/gen/proto/paxv1"
	cf "pax-connect/daemon/internal/cfoptimizer"
	"pax-connect/daemon/internal/measurement"
	"pax-connect/daemon/internal/network"
	"strings"
	"sync"
	"time"
)

var errCFTCPUnreachable = errors.New("cf_tcp_unreachable")
var errCFCacheSaveFailed = errors.New("cf_cache_save_failed")

// CFRepository is protected byte storage; the optimizer owns its versioned schema.
type CFRepository interface {
	Read() ([]byte, bool, error)
	Write(context.Context, []byte) error
}
type cfWorkspace struct {
	document                           cf.Document
	repository                         CFRepository
	probe                              func(network.Physical) cf.Probe
	saveMu                             sync.Mutex
	operation, phase, network, failure string
	completed, total                   int
	cancel                             context.CancelFunc
	proofs                             map[string]time.Time
	readOnly                           bool
	scan                               *cfScan
}

// ConfigureCFWorkspace loads historical observations, never resurrecting route proofs.
func (c *Controller) ConfigureCFWorkspace(repository CFRepository, probe func(network.Physical) cf.Probe) {
	w := &cfWorkspace{document: cf.NewDocument(), repository: repository, probe: probe, proofs: map[string]time.Time{}}
	if repository != nil {
		data, exists, err := repository.Read()
		if err != nil {
			w.failure = "cf_cache_invalid"
			w.readOnly = true
		} else if exists {
			d, e := cf.Decode(data)
			if e != nil {
				w.failure = "cf_cache_invalid"
				w.readOnly = true
			} else {
				w.document = d
				w.document.Prune(time.Now())
				if w.document.ScanNetwork != "" {
					w.phase = "scan_complete"
				}
			}
		}
	}
	c.cf.mu.Lock()
	c.cf.workspace = w
	c.cf.mu.Unlock()
}
func (c *Controller) saveCF(ctx context.Context) error {
	c.cf.mu.Lock()
	w := c.cf.workspace
	c.cf.mu.Unlock()
	if w == nil || w.repository == nil {
		return nil
	}
	w.saveMu.Lock()
	defer w.saveMu.Unlock()
	c.cf.mu.Lock()
	// A preceding replacement may have frozen writes while this save queued.
	if w.readOnly {
		c.cf.mu.Unlock()
		return errors.New("cf_cache_invalid")
	}
	d := w.document
	d.Records = append([]cf.Record(nil), w.document.Records...)
	c.cf.mu.Unlock()
	raw, err := cf.Encode(d)
	if err == nil {
		err = w.repository.Write(ctx, raw)
	}
	if err != nil {
		c.cf.mu.Lock()
		w.failure = "cf_cache_save_failed"
		c.cf.mu.Unlock()
		return errCFCacheSaveFailed
	}
	return nil
}
func (c *Controller) mergeCF(r cf.Record) {
	c.cf.mu.Lock()
	if w := c.cf.workspace; w != nil && w.document.Accepts(r.IP, r.Network) {
		w.document.Merge(r, time.Now())
	}
	c.cf.mu.Unlock()
}

// mergeCFExplicit stores the result of a user-requested single-IP test. Unlike late
// stage updates of a scan, the request names its own IP, so it need not be a scan member.
// The record keeps its own network tag; another network's scan records stay untouched.
func (c *Controller) mergeCFExplicit(r cf.Record) {
	c.cf.mu.Lock()
	if w := c.cf.workspace; w != nil {
		w.document.Merge(r, time.Now())
	}
	c.cf.mu.Unlock()
}
func (c *Controller) RunCFTask(ctx context.Context, r *pb.CFTaskRequest) (*pb.OperationAck, error) {
	if c.cf == nil || c.physical == nil || c.tests == nil {
		return nil, status.Error(codes.Unimplemented, "cf_workspace_unavailable")
	}
	for _, ip := range r.Ips {
		if !cf.IsAddress(ip) {
			return nil, status.Error(codes.InvalidArgument, "cf_ip_invalid")
		}
	}
	if len(r.Ips) > 4000 {
		return nil, status.Error(codes.InvalidArgument, "cf_candidate_limit")
	}
	if r.Action == "monitor_start" || r.Action == "monitor_stop" {
		return nil, status.Error(codes.Unimplemented, "cf_monitor_removed")
	}
	single := r.Action == "latency_one" || r.Action == "download_one" || r.Action == "measure_one"
	bandwidth := r.Action == "download" || r.Action == "download_one" || r.Action == "measure_one"
	if single && len(r.Ips) != 1 {
		return nil, status.Error(codes.InvalidArgument, "cf_candidate_limit")
	}
	if r.Action != "scan" && r.Action != "download" && r.Action != "check" && r.Action != "locate" && !single {
		return nil, status.Error(codes.InvalidArgument, "cf_action_invalid")
	}
	physical, err := c.physical.sample(ctx)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, "physical_network_unavailable")
	}
	if physical.IPv6State == "unknown" {
		return nil, status.Error(codes.FailedPrecondition, "network_observation_unavailable")
	}
	if physical.IPv4Only() && r.Action != "locate" {
		for _, ip := range r.Ips {
			if strings.Contains(ip, ":") {
				return nil, status.Error(codes.FailedPrecondition, "ipv6_unavailable")
			}
		}
	}
	_, _, downloadParallel := c.measurementParallel()
	c.cf.mu.Lock()
	w := c.cf.workspace
	if w == nil || w.probe == nil {
		c.cf.mu.Unlock()
		return nil, status.Error(codes.Unimplemented, "cf_workspace_unavailable")
	}
	if w.readOnly {
		c.cf.mu.Unlock()
		return nil, status.Error(codes.FailedPrecondition, w.failure)
	}
	if w.operation != "" {
		c.cf.mu.Unlock()
		return nil, status.Error(codes.ResourceExhausted, "cf_task_busy")
	}
	ips := []string{}
	seen := map[string]bool{}
	for _, ip := range r.Ips {
		if !seen[ip] {
			ips = append(ips, ip)
			seen[ip] = true
		}
	}
	if (r.Action == "download" || r.Action == "check" || r.Action == "locate") && len(ips) == 0 {
		c.cf.mu.Unlock()
		return nil, status.Error(codes.InvalidArgument, "cf_candidate_limit")
	}
	id, err := operationID()
	if err != nil {
		c.cf.mu.Unlock()
		return nil, err
	}
	c.tests.mu.Lock()
	if c.tests.closed {
		c.tests.mu.Unlock()
		c.cf.mu.Unlock()
		return nil, status.Error(codes.Unavailable, "daemon stopping")
	}
	if bandwidth && c.tests.bandwidth != "" {
		c.tests.mu.Unlock()
		c.cf.mu.Unlock()
		return nil, status.Error(codes.ResourceExhausted, "download_busy")
	}
	if bandwidth {
		c.tests.bandwidth = id
	}
	c.tests.probes.Add(1)
	c.tests.mu.Unlock()
	run, cancel := context.WithTimeout(c.tests.lifetime, 30*time.Minute)
	w.operation = id
	w.phase = r.Action
	w.cancel = cancel
	w.network = physical.Epoch
	w.completed = 0
	w.total = 0
	w.failure = ""
	if r.Action == "scan" {
		w.scan = &cfScan{network: physical.Epoch, records: []cf.Record{}}
	}
	c.cf.mu.Unlock()
	go c.runCF(run, cancel, id, r.Action, ips, physical, downloadParallel)
	return &pb.OperationAck{OperationId: id}, nil
}
func (c *Controller) runCF(ctx context.Context, cancel context.CancelFunc, id, action string, ips []string, physical network.Physical, downloadParallel int) {
	defer c.tests.probes.Done()
	defer cancel()
	c.cf.mu.Lock()
	w := c.cf.workspace
	probe := w.probe(physical)
	c.cf.mu.Unlock()
	// One joined watcher invalidates all in-flight probes when the physical path changes. It
	// follows the shared observer's epoch instead of sampling on its own clock.
	watcherDone := make(chan struct{})
	started := time.Now()
	go func() {
		defer close(watcherDone)
		if c.physical.waitEpochChange(ctx, physical.Epoch, started) {
			cancel()
		}
	}()
	lastPhase := ""
	progress := func(v cf.Progress) {
		if action != "scan" && lastPhase != "" && lastPhase != v.Phase {
			_ = c.saveCF(ctx)
		}
		lastPhase = v.Phase
		c.cf.mu.Lock()
		w.phase = v.Phase
		w.completed = v.Completed
		w.total = v.Total
		c.cf.mu.Unlock()
	}
	var err error
	switch action {
	case "latency_one", "download_one", "measure_one":
		err = c.measureCFSingle(ctx, physical.Epoch, ips[0], action, probe, progress)
	case "scan":
		if probe.TCP == nil {
			err = errors.New("cf_probe_unavailable")
		} else {
			err = c.runCFScan(ctx, id, physical, probe, progress)
		}
	case "download":
		if probe.Download == nil {
			err = errors.New("cf_probe_unavailable")
		} else if probe.TCP == nil {
			err = errors.New("cf_probe_unavailable")
		} else {
			reachable := []string{}
			err = cf.MeasureTCP(ctx, ips, physical.Epoch, 3, 4, probe, func(r cf.Record) {
				c.mergeCF(r)
				if r.Reachable {
					reachable = append(reachable, r.IP)
				} else {
					// This IP is terminal for the download request even though
					// other candidates will continue transferring data.
					c.mergeCF(cf.Record{IP: r.IP, Prefix: r.Prefix, Network: r.Network, Download: cf.Sample{Attempt: time.Now(), Error: "cf_tcp_unreachable"}})
				}
			}, func(v cf.Progress) { v.Phase = "check"; progress(v) })
			if err == nil && len(reachable) > 0 {
				for offset := 0; offset < len(reachable); offset += 20 {
					chunk := reachable[offset:min(offset+20, len(reachable))]
					err = cf.Download(ctx, physical.Epoch, chunk, downloadParallel, probe, c.mergeCF, func(v cf.Progress) { v.Completed += offset; v.Total = len(reachable); progress(v) })
					if err != nil {
						break
					}
				}
			}
			if err == nil && len(reachable) == 0 {
				err = errCFTCPUnreachable
			}
		}
	case "locate":
		err = c.locateCF(ctx, physical.Epoch, ips, probe, progress)
	case "check":
		if probe.TCP == nil {
			err = errors.New("cf_probe_unavailable")
		} else {
			err = cf.MeasureTCP(ctx, ips, physical.Epoch, 3, 4, probe, c.mergeCF, func(v cf.Progress) { v.Phase = "check"; progress(v) })
		}

	}
	cancel()
	<-watcherDone
	code := ""
	if err != nil {
		code = "cf_task_failed"
		if errors.Is(err, errCFCacheSaveFailed) {
			code = "cf_cache_save_failed"
		}
		if errors.Is(err, errCFTCPUnreachable) {
			code = "cf_tcp_unreachable"
		}
		if errors.Is(err, cf.ErrEnvironment) {
			code = "physical_network_unavailable"
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			code = "operation_cancelled"
		}
	}
	saveCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	var saveErr error
	if action != "scan" {
		saveErr = c.saveCF(saveCtx)
	}
	stop()
	if saveErr != nil {
		code = "cf_cache_save_failed"
	}
	c.cf.mu.Lock()
	if w.operation == id {
		w.operation = ""
		w.cancel = nil
		w.phase = "complete"
		if action == "scan" {
			w.phase = "scan_complete"
			if err != nil {
				w.phase = "scan_incomplete"
				if w.scan != nil {
					w.scan.records = nil
				}
			}
		}
		w.failure = code
	}
	c.cf.mu.Unlock()
	c.tests.mu.Lock()
	if c.tests.bandwidth == id {
		c.tests.bandwidth = ""
	}
	c.tests.mu.Unlock()
	c.completeOperation(id, "CF "+action, code)
}

// measureCFSingle keeps admission, path identity and cancellation under one owner.
func (c *Controller) measureCFSingle(ctx context.Context, network, ip, action string, p cf.Probe, progress func(cf.Progress)) error {
	c.cf.mu.Lock()
	known := false
	for _, r := range c.cf.workspace.document.Records {
		if r.IP == ip && r.Network == network && r.Reachable && r.TCP.Value > 0 && time.Since(r.CheckedAt) >= 0 && time.Since(r.CheckedAt) < 5*time.Minute {
			known = true
			break
		}
	}
	c.cf.mu.Unlock()
	if action != "download_one" || !known {
		if p.TCP == nil {
			return errors.New("cf_probe_unavailable")
		}
		reachable := false
		err := cf.MeasureTCP(ctx, []string{ip}, network, 3, 1, p, func(r cf.Record) { reachable = r.Reachable; c.mergeCFExplicit(r) }, func(v cf.Progress) { v.Phase = "single_check"; progress(v) })
		if err != nil {
			return err
		}
		if !reachable {
			if action == "download_one" || action == "measure_one" {
				c.mergeCFExplicit(cf.Record{IP: ip, Prefix: cf.Prefix(ip), Network: network, Download: cf.Sample{Attempt: time.Now(), Error: "cf_tcp_unreachable"}})
			}
			return errCFTCPUnreachable
		}
	}
	if action == "latency_one" {
		return nil
	}
	if p.Download == nil {
		return errors.New("cf_probe_unavailable")
	}
	return cf.Download(ctx, network, []string{ip}, 1, p, c.mergeCFExplicit, func(v cf.Progress) { v.Phase = "single_download"; progress(v) })
}

func (c *Controller) locateCF(ctx context.Context, network string, ips []string, p cf.Probe, progress func(cf.Progress)) error {
	if p.Locate == nil {
		return errors.New("cf_probe_unavailable")
	}
	jobs := make(chan string)
	results := make(chan cf.Record, 4)
	var workers sync.WaitGroup
	for i := 0; i < min(4, len(ips)); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for ip := range jobs {
				c.cf.mu.Lock()
				cached := cf.Location{}
				for _, row := range c.cf.workspace.document.Records {
					if row.IP == ip && row.Location.At.After(cached.At) {
						cached = row.Location
					}
				}
				c.cf.mu.Unlock()
				age := time.Since(cached.At)
				if !cached.At.IsZero() && cached.Error == "" && age >= 0 && age < 24*time.Hour {
					continue
				}
				bounded, stop := context.WithTimeout(ctx, measurement.TotalMeasureTimeout)
				value, err := p.Locate(bounded, ip)
				stop()
				if ctx.Err() != nil {
					continue
				}
				if err != nil {
					value = cf.Location{At: time.Now(), Error: "exit_location_unavailable"}
				}
				select {
				case results <- cf.Record{IP: ip, Prefix: cf.Prefix(ip), Network: network, Location: value}:
				case <-ctx.Done():
				}
			}
		}()
	}
	producer := make(chan struct{})
	go func() {
		defer close(producer)
		defer close(jobs)
		for _, ip := range ips {
			select {
			case jobs <- ip:
			case <-ctx.Done():
				return
			}
		}
	}()
	done := make(chan struct{})
	go func() { workers.Wait(); close(results); close(done) }()
	completed := 0
	for result := range results {
		c.mergeCFLocation(result)
		completed++
		progress(cf.Progress{Phase: "location", Completed: completed, Total: len(ips)})
	}
	<-producer
	<-done
	progress(cf.Progress{Phase: "location", Completed: len(ips), Total: len(ips)})
	return ctx.Err()
}

func (c *Controller) mergeCFLocation(in cf.Record) {
	c.cf.mu.Lock()
	defer c.cf.mu.Unlock()
	if !c.cf.workspace.document.Accepts(in.IP, in.Network) {
		return
	}
	found := false
	for i, r := range c.cf.workspace.document.Records {
		if r.IP == in.IP && r.Network == in.Network {
			c.cf.workspace.document.Records[i].Location = in.Location
			found = true
		}
	}
	if !found {
		c.cf.workspace.document.Merge(in, time.Now())
	}
}
