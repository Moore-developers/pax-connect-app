package controller

import (
	"context"
	"errors"
	"google.golang.org/protobuf/types/known/timestamppb"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"time"
)

// MeasurementRepository stores product values behind the serial catalog owner.
type MeasurementRepository interface {
	Load() (config.MeasurementDocument, error)
	Save(context.Context, config.MeasurementDocument) error
}

func (c *Controller) ConfigureMeasurements(store MeasurementRepository) {
	if c.proxy == nil {
		c.mu.Lock()
		c.measurements = store
		c.mu.Unlock()
		return
	}
	_ = c.query(c.proxy.lifetime, func(context.Context) { c.mu.Lock(); c.measurements = store; c.mu.Unlock(); c.loadMeasurements() })
}
func (c *Controller) loadMeasurements() {
	if c.measurements == nil || c.routes == nil {
		return
	}
	doc, err := c.measurements.Load()
	if err != nil {
		c.routes.measurementError = "measurement_store_failed"
		c.routes.measurementLoadFailed = true
		return
	}
	doc.MigrateExit()
	c.routes.measurementLoadFailed = false
	c.routes.measurementError = ""
	for id, record := range doc.Records {
		if _, ok := c.routes.catalog.Find(id); !ok {
			continue
		}
		if value := record.Latency; value != nil {
			c.routes.testResults[id] = routeTestResult{at: value.At, success: value.Success, duration: time.Duration(value.DurationMS) * time.Millisecond, code: value.Error, params: value.Params, exitIP: value.ExitIP, exitError: value.ExitError, revision: value.Revision, configRevision: value.ConfigRevision, measurement: config.RouteMeasurement{ExitLocation: value.ExitLocation, LastExitLocation: value.LastExitLocation, Stable: time.Duration(value.StableMS) * time.Millisecond, StableSamples: value.StableSamples, StableError: value.StableError, TestTarget: value.TestTarget, Method: value.Method, Cold: time.Duration(value.ColdMS) * time.Millisecond, Second: time.Duration(value.SecondMS) * time.Millisecond, Reused: value.Reused, FirstError: value.FirstError, SecondError: value.SecondError}}
		}
		if value := record.Exit; value != nil {
			c.routes.exitResults[id] = savedExitProto(value)
		}
		if value := record.Download; value != nil {
			result := &paxv1.DownloadTestSummary{At: timestamppb.New(value.At), Success: value.Success, Method: value.Method, Parallel: value.Parallel, SetupMs: value.SetupMS, EndReason: value.End, Bytes: value.Bytes, BytesPerSecond: value.BytesPerSecond, DurationMs: value.DurationMS, TestedConfigRevision: value.ConfigRevision}
			if value.Error != "" {
				result.Error = &paxv1.ProductError{Code: value.Error, MessageParams: value.Params}
			}
			c.routes.downloadResults[id] = result
		}
	}
}
func (c *Controller) persistMeasurements(ctx context.Context) {
	if c.measurementBatchHolds > 0 {
		return
	}
	if c.measurements == nil || c.routes == nil {
		return
	}
	if c.routes.measurementLoadFailed {
		return
	} // Preserve an unreadable document; never overwrite it with an empty replacement.
	doc := config.NewMeasurements()
	for _, route := range c.routes.catalog.Routes {
		id := route.Config.ID
		record := config.MeasurementRecord{}
		if value := c.routes.exitResults[id]; value != nil {
			record.Exit = savedExit(value)
		}
		if value, ok := c.routes.testResults[id]; ok && !value.at.IsZero() && value.configRevision != 0 {
			record.Latency = &config.SavedMeasurement{ExitLocation: value.measurement.ExitLocation, LastExitLocation: value.measurement.LastExitLocation, StableMS: uint32(value.measurement.Stable.Milliseconds()), StableSamples: value.measurement.StableSamples, StableError: value.measurement.StableError, TestTarget: value.measurement.TestTarget, Method: value.measurement.Method, ColdMS: uint32(value.measurement.Cold.Milliseconds()), SecondMS: uint32(value.measurement.Second.Milliseconds()), Reused: value.measurement.Reused, FirstError: value.measurement.FirstError, SecondError: value.measurement.SecondError, At: value.at, ConfigRevision: value.configRevision, Revision: value.revision, Success: value.success, DurationMS: uint32(value.duration.Milliseconds()), Error: value.code, Params: value.params, ExitIP: value.exitIP, ExitError: value.exitError}
		}
		if value, ok := c.routes.downloadResults[id]; ok {
			record.Download = &config.SavedMeasurement{Method: value.Method, Parallel: value.Parallel, SetupMS: value.SetupMs, End: value.EndReason, At: value.At.AsTime(), ConfigRevision: value.TestedConfigRevision, Success: value.Success, DurationMS: value.DurationMs, Bytes: value.Bytes, BytesPerSecond: value.BytesPerSecond, Error: value.GetError().GetCode(), Params: value.GetError().GetMessageParams()}
		}
		if record.Latency != nil || record.Exit != nil || record.Download != nil {
			doc.Records[id] = record
		}
	}
	if err := c.measurements.Save(ctx, doc); err != nil {
		c.routes.measurementError = "measurement_store_failed"
		if errors.Is(err, config.ErrMeasurementDurability) {
			c.routes.measurementError = "measurement_store_uncertain"
		}
	} else {
		c.routes.measurementError = ""
	}
}
