package controller

import (
	"context"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/network"
)

const pathNetworkWait = 10 * time.Second
const pathExecution = 5 * time.Second
const pathFollowupTTL = time.Minute
const pathStableInterval = 2 * time.Second
const pathSampleFreshness = 3 * time.Second

// localNetworkSample is independent of the public-IP provider/cache result.
// The existing physical observer is its sole producer under physicalOwner.mu.
type localNetworkSample struct {
	at, previous, stableSince time.Time
	epoch                     string
	valid                     bool
	generation                uint64
}

func (p *physicalOwner) publishLocalSample(value network.Physical, err error) {
	now := time.Now()
	valid := err == nil && value.Name != "" && value.Index > 0 && value.Epoch != ""
	p.mu.Lock()
	old := p.local
	generation := old.generation
	since := old.stableSince
	if old.valid != valid || old.epoch != value.Epoch || generation == 0 || now.Sub(old.at) > pathSampleFreshness {
		generation++
		since = now
	}
	p.local = localNetworkSample{at: now, previous: old.at, stableSince: since, epoch: value.Epoch, valid: valid, generation: generation}
	if p.changed != nil {
		close(p.changed)
	}
	p.changed = make(chan struct{})
	p.mu.Unlock()
}

func (p *physicalOwner) localSnapshot() (localNetworkSample, <-chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.local, p.changed
}

func (s localNetworkSample) ready(after, now time.Time) bool {
	return s.valid && !s.previous.Before(after) && !s.previous.Before(s.stableSince) &&
		s.at.Sub(s.previous) >= pathStableInterval && now.Sub(s.at) <= pathSampleFreshness
}

func waitLocalNetwork(ctx context.Context, p *physicalOwner, after time.Time) (localNetworkSample, error) {
	if p == nil {
		return localNetworkSample{}, context.DeadlineExceeded
	}
	var first time.Time
	var generation uint64
	for {
		value, changed := p.localSnapshot()
		if value.generation != generation {
			first = time.Time{}
			generation = value.generation
		}
		if value.valid && !value.at.Before(after) && time.Since(value.at) <= pathSampleFreshness && first.IsZero() {
			first = value.at
		}
		if value.ready(after, time.Now()) || value.valid && !first.IsZero() && value.at.Sub(first) >= pathStableInterval && time.Since(value.at) <= pathSampleFreshness {
			return value, nil
		}
		select {
		case <-ctx.Done():
			return value, ctx.Err()
		case <-changed:
		}
	}
}

type wakeFollowup struct {
	binding    runningBinding
	sequence   uint64
	at         time.Time
	first      time.Time
	generation uint64
}

func (c *Controller) localNetworkChanged() {
	p := c.proxy
	if c.physical == nil {
		return
	}
	sample, _ := c.physical.localSnapshot()
	if sample.generation != p.networkGeneration {
		previous := p.networkGeneration
		p.networkGeneration = sample.generation
		if previous != 0 {
			now := time.Now()
			c.enqueueHistoryEvent(config.HistoryEvent{ID: "network:" + now.Format("20060102150405.000000000"), At: now, Kind: "network_changed", Source: "Pax", Level: "info", Title: "网络环境变化", System: &config.SystemEvent{Type: "runtime.network_changed", Name: "网络环境变化", Category: config.EventCategory{Key: "runtime", Name: "网络与后台", Symbol: "network"}, Result: "completed"}})
		}
		// A wake worker owns its bounded stable-sample wait; do not cancel it for
		// the very interface transition it is waiting for.
		if p.pathCheck == nil || !p.pathCheck.waitNetwork {
			followup := p.wakeFollowup
			cause := "network_changed"
			if p.sleeping {
				cause = "device_sleeping"
			}
			c.cancelCurrentPath(cause)
			p.wakeFollowup = followup
		}
	}
	c.resumeWakeFollowup()
}

func (c *Controller) resumeWakeFollowup() {
	p := c.proxy
	follow := p.wakeFollowup
	if follow == nil {
		return
	}
	if time.Since(follow.at) >= pathFollowupTTL || p.sleeping || !p.desired.Load() || p.running != follow.binding || p.powerSequence != follow.sequence {
		p.wakeFollowup = nil
		return
	}
	if p.pathCheck != nil || c.physical == nil {
		return
	}
	sample, _ := c.physical.localSnapshot()
	if sample.generation != follow.generation {
		follow.first = time.Time{}
		follow.generation = sample.generation
	}
	if sample.valid && !sample.at.Before(follow.at) && time.Since(sample.at) <= pathSampleFreshness && follow.first.IsZero() {
		follow.first = sample.at
	}
	ready := sample.ready(follow.at, time.Now()) || sample.valid && !follow.first.IsZero() && sample.at.Sub(follow.first) >= pathStableInterval && time.Since(sample.at) <= pathSampleFreshness
	if !ready {
		return
	}
	p.wakeFollowup = nil // consume before enqueue; failure never returns the token
	c.beginPathCheck("", "network_return", false, follow.at)
}
