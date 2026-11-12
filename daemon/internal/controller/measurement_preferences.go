package controller

import (
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
	"sync"
	"time"
)

// PreferenceRepository is the consumer-owned protected storage boundary.
type PreferenceRepository interface {
	Load() (config.MeasurementPreferences, bool, error)
	Save(context.Context, config.MeasurementPreferences) error
}
type preferenceOwner struct {
	mu       sync.Mutex
	value    config.MeasurementPreferences
	store    PreferenceRepository
	hardware int
	failure  bool
}

func (c *Controller) ConfigureMeasurementPreferences(store PreferenceRepository, hardware int) {
	value := config.MeasurementPreferences{Revision: 1, Latency: hardware, Exit: 8, Download: 8, Mode: "default", LatencyMode: "default", ExitMode: "default", DownloadMode: "default"}
	saved, exists, err := store.Load()
	if exists && err == nil {
		value = saved
	}
	c.preferences = &preferenceOwner{value: value, store: store, hardware: hardware, failure: err != nil}
	if c.exits != nil {
		c.exits.SetLimit(exitPreferenceLimit(value), value.Revision)
	}
}
func preferenceProto(v config.MeasurementPreferences) *paxv1.MeasurementPreferences {
	value := &paxv1.MeasurementPreferences{LatencyMode: v.LatencyMode, ExitMode: v.ExitMode, DownloadMode: v.DownloadMode, Revision: v.Revision, LatencyParallel: uint32(v.Latency), ExitParallel: uint32(v.Exit), DownloadParallel: uint32(v.Download), Mode: v.Mode, RecommendedLatency: uint32(v.RecommendedLatency), RecommendedExit: uint32(v.RecommendedExit), RecommendedDownload: uint32(v.RecommendedDownload), RecommendationNetwork: v.Network}
	if v.EvaluatedAt > 0 {
		value.EvaluatedAt = timestamppb.New(time.Unix(v.EvaluatedAt, 0))
	}
	return value
}
func (c *Controller) GetMeasurementPreferences(context.Context, *emptypb.Empty) (*paxv1.MeasurementPreferences, error) {
	if c.preferences == nil {
		return preferenceProto(config.MeasurementPreferences{Revision: 1, Latency: maxParallelTests, Exit: 8, Download: 8, Mode: "default", LatencyMode: "default", ExitMode: "default", DownloadMode: "default"}), nil
	}
	p := c.preferences
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failure {
		return nil, status.Error(codes.FailedPrecondition, "measurement_preferences_invalid")
	}
	value := preferenceProto(p.value)
	value.ExitPoolState = c.exitPoolProto()
	return value, nil
}
func (c *Controller) SaveMeasurementPreferences(ctx context.Context, request *paxv1.MeasurementPreferences) (*paxv1.MeasurementPreferences, error) {
	if c.preferences == nil {
		return nil, status.Error(codes.Unimplemented, "measurement_preferences_unavailable")
	}
	p := c.preferences
	p.mu.Lock()
	locked := true
	defer func() {
		if locked {
			p.mu.Unlock()
		}
	}()
	if p.failure {
		return nil, status.Error(codes.FailedPrecondition, "measurement_preferences_invalid")
	}
	if p.value.Revision != request.Revision {
		return nil, status.Error(codes.FailedPrecondition, "measurement_preferences_stale")
	}
	value := p.value
	value.Latency = int(request.LatencyParallel)
	value.Exit = int(request.ExitParallel)
	value.Download = int(request.DownloadParallel)
	value.Mode = request.Mode
	value.LatencyMode = request.LatencyMode
	value.ExitMode = request.ExitMode
	value.DownloadMode = request.DownloadMode
	if value.LatencyMode == "" {
		value.LatencyMode = value.Mode
	}
	if value.ExitMode == "" {
		value.ExitMode = value.Mode
	}
	if value.DownloadMode == "" {
		value.DownloadMode = value.Mode
	}
	if value.LatencyMode == "default" {
		value.Latency = p.hardware
	}
	if value.ExitMode != "manual" {
		value.Exit = 8
	}
	if value.DownloadMode == "default" {
		value.Download = 8
	}
	value.Revision++
	if value.Validate() != nil {
		return nil, status.Error(codes.InvalidArgument, "measurement_preferences_invalid")
	}
	if err := p.store.Save(ctx, value); err != nil {
		return nil, status.Error(codes.Internal, "measurement_preferences_save_failed")
	}
	p.value = value
	p.mu.Unlock()
	locked = false
	if c.exits != nil {
		c.applyExitLimit()
		c.scheduleMeasurementPublish()
	}
	reply := preferenceProto(value)
	reply.ExitPoolState = c.exitPoolProto()
	return reply, nil
}
func (c *Controller) measurementParallel() (int, int, int) {
	latency, exit, download := maxParallelTests, 8, 8
	if c.tests != nil {
		latency = c.tests.latencyParallel
	}
	if p := c.preferences; p != nil {
		p.mu.Lock()
		v := p.value
		p.mu.Unlock()
		latency, exit, download = v.Latency, v.Exit, v.Download
		current := ""
		if c.physical != nil {
			c.physical.mu.Lock()
			current = c.physical.current.Network
			c.physical.mu.Unlock()
		}
		fresh := current != "" && current == v.Network && v.RecommendedDownload > 0
		lm, em, dm := v.LatencyMode, v.ExitMode, v.DownloadMode
		if lm == "" {
			lm = v.Mode
		}
		if em == "" {
			em = v.Mode
		}
		if dm == "" {
			dm = v.Mode
		}
		if lm == "auto" {
			latency = p.hardware
			if fresh {
				latency = v.RecommendedLatency
			}
		}
		if em != "manual" {
			exit = 8
		}
		if dm == "auto" {
			download = 8
			if fresh {
				download = v.RecommendedDownload
			}
		}

	}
	return latency, exit, download
}

func (c *Controller) acquireLatency(ctx context.Context, owner *testOwner, limit int) error {
	for {
		owner.mu.Lock()
		value := &owner.activeLatency
		if *value < limit {
			*value++
			owner.mu.Unlock()
			return nil
		}
		owner.mu.Unlock()
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func (c *Controller) releaseLatency(owner *testOwner) {
	owner.mu.Lock()
	owner.activeLatency--
	owner.mu.Unlock()
}
func exitPreferenceLimit(v config.MeasurementPreferences) int {
	mode := v.ExitMode
	if mode == "" {
		mode = v.Mode
	}
	if mode == "manual" {
		return v.Exit
	}
	return 8
}
func (c *Controller) applyExitLimit() {
	if c.preferences == nil {
		return
	}
	c.preferences.mu.Lock()
	value := c.preferences.value
	c.preferences.mu.Unlock()
	c.exits.SetLimit(exitPreferenceLimit(value), value.Revision)
}

// ConfigureExitLookup installs the same concrete scheduler used by route and physical clients.
func (c *Controller) ConfigureExitLookup(lookup *measurement.ExitLookup) {
	c.mu.Lock()
	c.exits = lookup
	c.mu.Unlock()
	if c.preferences != nil {
		c.applyExitLimit()
	}
}
