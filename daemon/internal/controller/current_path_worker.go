package controller

import (
	"context"
	"errors"
	"time"

	"pax-connect/daemon/internal/config"
)

func (c *Controller) currentPathWorker() {
	p := c.proxy
	defer close(p.pathWorkerDone)
	for {
		select {
		case <-p.lifetime.Done():
			return
		case job := <-p.pathJobs:
			if job.ctx.Err() != nil {
				continue
			}
			done := c.runPathCheck(job)
			select {
			case p.pathCompleted <- done:
			case <-p.lifetime.Done():
				return
			}
		}
	}
}

func (c *Controller) runPathCheck(job pathCheckJob) pathCheckDone {
	done := pathCheckDone{job: job, result: config.CurrentPathResult{CheckID: job.checkID, State: "inconclusive", Cause: "current_path_unavailable"}}
	// A tag alone cannot prove which instance committed it. Reconnection must
	// establish the binding; a worker never adopts a replacement core.
	if job.binding.session == "" {
		done.result.Cause = "runtime_binding_unconfirmed"
		return done
	}
	if job.waitNetwork {
		wait, cancel := context.WithDeadline(job.ctx, job.wakeAt.Add(pathNetworkWait))
		sample, err := waitLocalNetwork(wait, c.physical, job.wakeAt)
		cancel()
		if err != nil {
			done.result.Cause = "physical_network_wait"
			done.result.CompletedAt = time.Now()
			return done
		}
		done.network = sample.epoch
		done.generation = sample.generation
	}
	observer, ok := c.coreSession.(coreObserver)
	if !ok {
		done.result.Cause = "runtime_binding_unconfirmed"
		return done
	}
	// Wake preparation shares the ten-second local readiness phase, keeping
	// the full five-second HTTP budget independent of observation latency.
	observeParent := job.ctx
	var prepareDone context.CancelFunc = func() {}
	if job.waitNetwork {
		observeParent, prepareDone = context.WithDeadline(job.ctx, job.wakeAt.Add(pathNetworkWait))
	}
	defer prepareDone()
	observe, cancel := context.WithTimeout(observeParent, time.Second)
	actual, err := observer.ObserveCore(observe)
	cancel()
	if err != nil {
		done.result.Cause = "runtime_binding_unconfirmed"
		return done
	}
	if actual.Status != "STARTED" {
		done.result.Cause = "core_not_started"
		return done
	}
	if actual.SelectedTag != job.binding.tag || job.binding.session != "" && actual.RuntimeSession != job.binding.session {
		done.result.State = "stale"
		done.result.Cause = "current_path_stale"
		return done
	}
	if actual.RuntimeSession == "" || actual.RuntimeVersion == 0 {
		done.result.Cause = "runtime_binding_unconfirmed"
		return done
	}
	if !job.waitNetwork {
		if c.physical != nil {
			sample, _ := c.physical.localSnapshot()
			done.network = sample.epoch
			done.generation = sample.generation
		}
		if done.network == "" && c.proxy.networkEpoch != nil {
			done.network, _ = c.proxy.networkEpoch()
		}
	}
	started := time.Now()
	progress := pathCheckDone{job: job, progress: true, network: done.network, generation: done.generation, result: config.CurrentPathResult{State: "checking", StartedAt: started}}
	select {
	case c.proxy.pathCompleted <- progress:
	case <-job.ctx.Done():
		return done
	}
	probe, stop := context.WithTimeout(job.ctx, pathExecution)
	checker, ok := c.coreSession.(interface {
		VerifyCurrentPath(context.Context, config.CurrentPathRequest) (config.CurrentPathResult, error)
	})
	if ok {
		value, e := checker.VerifyCurrentPath(probe, config.CurrentPathRequest{CheckID: job.checkID, ExpectedTag: job.binding.tag, ConfigRevision: job.binding.revision, ExpectedSession: actual.RuntimeSession, ExpectedVersion: actual.RuntimeVersion})
		if e == nil {
			done.result = value
		} else if errors.Is(probe.Err(), context.DeadlineExceeded) {
			done.result.Cause = "check_timeout"
		}
	}
	stop()
	if done.result.StartedAt.IsZero() {
		done.result.StartedAt = started
	}
	done.result.CompletedAt = time.Now()
	// Fresh validation is independent of the expired HTTP context. Local reads
	// never perform an exit-IP lookup or a network lifecycle mutation.
	if c.physical != nil {
		sample, _ := c.physical.localSnapshot()
		if sample.generation != done.generation || sample.epoch != done.network {
			done.result.State = "stale"
			done.result.Cause = "network_changed"
		} else if !sample.valid || time.Since(sample.at) > pathSampleFreshness {
			done.result.State = "inconclusive"
			done.result.Cause = "network_unconfirmed"
		}
	} else if c.proxy.networkEpoch != nil {
		latest, e := c.proxy.networkEpoch()
		if e != nil || done.network == "" {
			done.result.State = "inconclusive"
			done.result.Cause = "network_unconfirmed"
		} else if latest != done.network {
			done.result.State = "stale"
			done.result.Cause = "network_changed"
		}
	} else if done.result.State == "passed" {
		done.result.State = "inconclusive"
		done.result.Cause = "network_unconfirmed"
	}
	return done
}
