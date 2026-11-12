package controller

import (
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
	"sync"
	"time"
)

// EvaluateMeasurementPreferences is explicit bounded network work, independent
// from App lifetime. Eight routes x three levels x two rounds <=384MiB payload.
func (c *Controller) EvaluateMeasurementPreferences(ctx context.Context, req *paxv1.TestRouteRequest) (*paxv1.OperationAck, error) {
	downloader, ok := c.tester.(RouteDownloader)
	if !ok || c.preferences == nil || c.tests == nil || c.physical == nil || len(req.Targets) < 2 || len(req.Targets) > 8 {
		return nil, status.Error(codes.FailedPrecondition, "assessment_unavailable")
	}
	physical, err := c.physical.sample(ctx)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, "physical_network_unavailable")
	}
	id, err := operationID()
	if err != nil {
		return nil, err
	}
	profiles := []config.RouteConfig{}
	var failure error
	err = c.query(ctx, func(ctx context.Context) {
		seen := map[string]bool{}
		for _, target := range req.Targets {
			record, found := c.routes.catalog.Find(target.RouteId)
			if !found || record.Hidden || seen[target.RouteId] || record.Revision != target.ExpectedRouteRevision {
				failure = status.Error(codes.FailedPrecondition, "route_stale")
				return
			}
			seen[target.RouteId] = true
			resolved, e := c.resolvedRecord(ctx, record)
			if e != nil {
				failure = e
				return
			}
			profile := resolved.Config
			if profile.TLSResolved.NeedsConfirmation {
				failure = status.Error(codes.FailedPrecondition, "tls_confirmation_required")
				return
			}
			if target.ExpectedConfigRevision != 0 && target.ExpectedConfigRevision != profile.ResolvedConfigRevision {
				failure = status.Error(codes.FailedPrecondition, "route_stale")
				return
			}
			profiles = append(profiles, profile)
		}
		owner := c.tests
		owner.mu.Lock()
		defer owner.mu.Unlock()
		if owner.closed || owner.bandwidth != "" {
			failure = status.Error(codes.ResourceExhausted, "download_busy")
			return
		}
		task, cancel := context.WithTimeout(owner.lifetime, 8*time.Minute)
		owner.bandwidth = id
		owner.ops[id] = cancel
		owner.probes.Add(1)
		c.preferences.mu.Lock()
		revision := c.preferences.value.Revision
		c.preferences.mu.Unlock()
		c.mu.Lock()
		c.assessmentID = id
		c.current.DownloadOperationId = id
		c.commitLocked()
		c.mu.Unlock()
		go c.runAssessment(task, cancel, profiles, downloader, id, physical.Epoch, revision)
	})
	if err != nil {
		return nil, err
	}
	if failure != nil {
		return nil, failure
	}
	return &paxv1.OperationAck{OperationId: id}, nil
}
func (c *Controller) runAssessment(ctx context.Context, cancel context.CancelFunc, profiles []config.RouteConfig, downloader RouteDownloader, id, epoch string, revision uint64) {
	defer c.tests.probes.Done()
	cleanup := func() {
		cancel()
		c.tests.mu.Lock()
		delete(c.tests.ops, id)
		if c.tests.bandwidth == id {
			c.tests.bandwidth = ""
		}
		c.tests.mu.Unlock()
	}
	type score struct {
		valid   int
		bytes   uint64
		elapsed time.Duration
	}
	scores := map[int]score{}
	failure := ""
	for round := 0; round < 2; round++ {
		levels := []int{2, 4, 8}
		if round == 1 {
			levels = []int{8, 4, 2}
		}
		for _, parallel := range levels {
			if ctx.Err() != nil {
				failure = "operation_cancelled"
				break
			}
			current, e := c.physical.sample(ctx)
			if e != nil || current.Epoch != epoch {
				failure = "network_changed"
				break
			}
			start := time.Now()
			jobs := make(chan config.RouteConfig, len(profiles))
			for _, profile := range profiles {
				jobs <- profile
			}
			close(jobs)
			var done sync.WaitGroup
			var mu sync.Mutex
			stage := score{}
			for i := 0; i < min(parallel, len(profiles)); i++ {
				done.Add(1)
				go func() {
					defer done.Done()
					for profile := range jobs {
						profile = bindRouteNetwork(profile, current)
						if ctx.Err() != nil {
							return
						}
						bounded, stop := context.WithTimeout(ctx, measurement.DownloadTimeout)
						select {
						case <-c.tests.slots:
						case <-bounded.Done():
							stop()
							return
						}
						value, err := downloader.DownloadRoute(bounded, profile)
						c.tests.slots <- struct{}{}
						stop()
						if err == nil && value.Bytes >= 256*1024 && value.Duration >= time.Second {
							mu.Lock()
							stage.valid++
							stage.bytes += value.Bytes
							mu.Unlock()
						}
					}
				}()
			}
			done.Wait()
			stage.elapsed = time.Since(start)
			total := scores[parallel]
			total.valid += stage.valid
			total.bytes += stage.bytes
			total.elapsed += stage.elapsed
			scores[parallel] = total
		}
		if failure != "" {
			break
		}
	}
	if ctx.Err() != nil {
		failure = "operation_cancelled"
	}
	if failure == "" {
		current, e := c.physical.sample(ctx)
		if e != nil || current.Epoch != epoch {
			failure = "network_changed"
		}
	}
	recommendation := 0
	best := 0.0
	if failure == "" {
		bestValid := 0
		for _, s := range scores {
			bestValid = max(bestValid, s.valid)
		}
		for _, parallel := range []int{2, 4, 8} {
			s := scores[parallel]
			if s.valid < max(1, len(profiles)*2*3/4) || s.valid < bestValid-1 || s.elapsed <= 0 {
				continue
			}
			rate := float64(s.bytes) / s.elapsed.Seconds()
			if rate > best*1.1 {
				recommendation = parallel
				best = rate
			}
		}
		if recommendation == 0 {
			failure = "assessment_unstable"
		}
	}
	finalFailure := failure
	taskContext := ctx
	if !c.enqueueCatalogOp(id, "并发评估", func(ctx context.Context) error {
		defer cleanup()
		c.assessmentID = ""
		c.mu.Lock()
		if c.current.DownloadOperationId == id {
			c.current.DownloadOperationId = ""
		}
		c.commitLocked()
		c.mu.Unlock()
		if finalFailure != "" {
			return &productError{code: finalFailure}
		}
		if taskContext.Err() != nil {
			return &productError{code: "operation_cancelled"}
		}
		verify, stop := context.WithTimeout(ctx, 2*time.Second)
		latest, err := c.physical.sample(verify)
		stop()
		if err != nil || latest.Epoch != epoch {
			return &productError{code: "network_changed"}
		}
		for _, profile := range profiles {
			record, exists := c.routes.catalog.Find(profile.ID)
			if !exists || record.Hidden {
				return &productError{code: "route_stale"}
			}
			resolved, e := c.resolvedRecord(ctx, record)
			if e != nil || resolved.Config.ResolvedConfigRevision != profile.ResolvedConfigRevision {
				return &productError{code: "route_stale"}
			}
		}

		p := c.preferences
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.value.Revision != revision {
			return &productError{code: "measurement_preferences_stale"}
		}
		value := p.value
		value.RecommendedLatency = p.hardware
		value.RecommendedExit = 0
		value.RecommendedDownload = recommendation
		value.Network = epoch
		value.EvaluatedAt = time.Now().Unix()
		value.Revision++
		if p.store.Save(ctx, value) != nil {
			return &productError{code: "measurement_preferences_save_failed"}
		}
		p.value = value
		return nil
	}) {
		cleanup()
		c.completeOperation(id, "并发评估", "operation_cancelled")
	}
}
