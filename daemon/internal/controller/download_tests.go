package controller

import (
	"context"
	"errors"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"math"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
	"pax-connect/daemon/internal/singbox"
	"pax-connect/daemon/internal/telemetry"
	"time"
)

// RouteDownloader is the bounded isolated protocol boundary for explicit downloads.
type RouteDownloader interface {
	DownloadRoute(context.Context, config.RouteConfig) (config.DownloadMeasurement, error)
}

func (c *Controller) testDownload(ctx context.Context, req *paxv1.TestRouteRequest) (*paxv1.OperationAck, error) {
	downloader, ok := c.tester.(RouteDownloader)
	if !ok || c.tests == nil || c.routes == nil || c.proxy == nil {
		return nil, status.Error(codes.FailedPrecondition, "download_unavailable")
	}
	var profile config.RouteConfig
	var revision uint64
	var ack *paxv1.OperationAck
	var failure error
	if err := c.query(ctx, func(ctx context.Context) {
		record, ok := c.routes.catalog.Find(req.RouteId)
		if !ok || record.Hidden {
			failure = status.Error(codes.NotFound, "route_not_found")
			return
		}
		if record.Revision != req.ExpectedRouteRevision {
			failure = status.Error(codes.FailedPrecondition, "route_stale")
			return
		}
		if id := c.routes.downloading[req.RouteId]; id != "" {
			ack = &paxv1.OperationAck{OperationId: id}
			return
		}
		resolved, resolveErr := c.resolvedRecord(ctx, record)
		if resolveErr != nil {
			failure = resolveErr
			return
		}
		record = resolved
		profile = record.Config
		view := profile.TLSResolved
		if record.Chain == nil && c.tls != nil && profile.Security == "tls" {
			resolved, err := c.tls.Resolve(ctx, profile)
			if err != nil {
				failure = status.Error(codes.FailedPrecondition, tlsErrorCode(err))
				return
			}
			view = resolved
		}
		if view.NeedsConfirmation {
			failure = status.Error(codes.FailedPrecondition, "tls_confirmation_required")
			return
		}
		profile.TLSResolved = view
		profile.MeasurementParallel = 1
		revision = connectionVersion(record, view)
		id, err := operationID()
		if err != nil {
			failure = status.Error(codes.Internal, "operation id unavailable")
			return
		}
		owner := c.tests
		owner.mu.Lock()
		locked := true
		defer func() {
			if locked {
				owner.mu.Unlock()
			}
		}()
		if owner.closed {
			failure = status.Error(codes.Unavailable, "daemon stopping")
			return
		}
		if owner.bandwidth != "" && owner.bandwidth != "single-download-queue" {
			failure = status.Error(codes.ResourceExhausted, "download_busy")
			return
		}
		if len(owner.singleDownloads) >= 1000 {
			failure = status.Error(codes.ResourceExhausted, "download_queue_full")
			return
		}
		task, cancel := context.WithCancel(owner.lifetime)
		owner.bandwidth = "single-download-queue"
		owner.singleDownloads = append(owner.singleDownloads, singleDownload{ctx: task, cancel: cancel, profile: profile, revision: revision, id: id})

		owner.ops[id] = func() { cancel(); c.scheduleMeasurementPublish() }
		c.routes.downloading[profile.ID] = id
		owner.mu.Unlock()
		locked = false
		c.startSingleDownload(owner, downloader)
		c.publishCatalog(ctx)
		ack = &paxv1.OperationAck{OperationId: id}
	}); err != nil {
		return nil, err
	}
	return ack, failure
}
func (c *Controller) runDownload(ctx context.Context, cancel context.CancelFunc, owner *testOwner, downloader RouteDownloader, profile config.RouteConfig, revision uint64, id string) string {
	defer owner.probes.Done()
	defer cancel()
	var value config.DownloadMeasurement
	admitted := false
	select {
	case <-owner.slots:
		admitted = true
	case <-ctx.Done():
	}
	if admitted {
		defer func() { owner.slots <- struct{}{} }()
	}
	ctx, stopExecution := context.WithTimeout(ctx, measurement.DownloadTimeout)
	defer stopExecution()
	networkBefore := ""
	if c.physical != nil {
		native, e := c.physical.sample(ctx)
		if e == nil {
			networkBefore = native.Epoch
			profile = bindRouteNetwork(profile, native)
		}
	}
	err := ctx.Err()
	if err == nil {
		value, err = downloader.DownloadRoute(ctx, profile)
	}
	code := ""
	var params []string
	if err == nil && (value.Bytes < 256*1024 || value.Bytes > measurement.DownloadBudget || value.Duration < time.Second || value.BytesPerSecond <= 0 || math.IsNaN(value.BytesPerSecond) || math.IsInf(value.BytesPerSecond, 0)) {
		err = singbox.ErrDownloadIncomplete
	}
	if errors.Is(err, config.ErrIPv6Unavailable) {
		code = "ipv6_unavailable"
	} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		code = "download_timeout"
	} else if errors.Is(ctx.Err(), context.Canceled) {
		code = "operation_cancelled"
	} else if err != nil {
		var certificate *singbox.CertificateError
		if errors.As(err, &certificate) {
			code = "tls_certificate_invalid"
			params = []string{certificate.Reason}
		} else if errors.Is(err, singbox.ErrDownloadIncomplete) {
			code = "download_incomplete"
		} else {
			var failure *measurement.Failure
			if errors.As(err, &failure) {
				code = failure.Code
				if failure.Reason != "" {
					params = []string{failure.Reason}
				}
			} else {
				code = "download_failed"
			}
		}
	}
	parallel := profile.MeasurementParallel
	if networkBefore != "" && ctx.Err() == nil {
		latest, e := c.physical.sample(ctx)
		if e != nil || latest.Epoch != networkBefore {
			code = "network_changed"
		}
	}
	if parallel == 0 {
		parallel = 1
	}
	result := &paxv1.DownloadTestSummary{Method: "window6s-v2", Parallel: uint32(parallel), SetupMs: uint32(value.Setup.Milliseconds()), EndReason: value.End, Bytes: value.Bytes, DurationMs: uint32(value.Duration.Milliseconds()), At: timestamppb.Now(), Success: code == "", TestedConfigRevision: revision}
	if code == "" {
		result.Bytes = value.Bytes
		result.DurationMs = uint32(value.Duration.Milliseconds())
		result.BytesPerSecond = value.BytesPerSecond
	} else {
		result.Error = &paxv1.ProductError{Code: code, MessageParams: params}
	}
	telemetry.RecordDiagnostic(telemetry.Diagnostic{OperationID: id, Stage: "route_download", Phase: value.End, Code: code, DurationMS: value.Duration.Milliseconds(), Success: code == ""})
	evidence := operationEvidence("下载测速", code, "", "")
	speed := sampleText(result.Success, fmt.Sprintf("%.0f B/s", result.BytesPerSecond))
	evidence.Sections = []config.EventSection{{Kind: "metrics", Title: "当次下载测试", Fields: []config.EventField{{Label: "线路", Value: profile.Name}, {Label: "速度", Value: speed}, {Label: "样本时间", Value: result.At.AsTime().Format(time.RFC3339)}}}}
	if !c.enqueueCatalogEvidence(id, "下载测速", evidence, func(ctx context.Context) error {
		delete(c.routes.downloading, profile.ID)
		if _, ok := c.routes.catalog.Find(profile.ID); ok && code != "ipv6_unavailable" {
			c.routes.downloadResults[profile.ID] = result
			c.appendHistory(ctx, config.HistorySample{RouteID: profile.ID, Metric: "download", Network: networkBefore, At: result.At.AsTime(), ConfigRevision: revision, Value: result.BytesPerSecond, Success: result.Success, Error: code})
		}
		c.persistMeasurements(ctx)
		owner.mu.Lock()
		single := owner.singleDownloadActive == id
		if single {
			owner.singleDownloadActive = ""
		}
		owner.mu.Unlock()
		if single {
			c.startSingleDownload(owner, downloader)
		}
		c.publishCatalog(ctx)
		if code != "" {
			return &productError{code: code, params: params}
		}
		return nil
	}) {
		c.completeOperation(id, "下载测速", "operation_cancelled")
	}
	owner.mu.Lock()
	delete(owner.ops, id)
	if owner.bandwidth == id {
		owner.bandwidth = ""
	}
	owner.mu.Unlock()
	return code
}

// singleDownload freezes a queued user's configuration without opening a native connection.
type singleDownload struct {
	ctx      context.Context
	cancel   context.CancelFunc
	profile  config.RouteConfig
	revision uint64
	id       string
}

// Called only by the product owner. A single user download runs at a time;
// batch/CF/assessment bandwidth owners cannot overlap this queue.
func (c *Controller) startSingleDownload(owner *testOwner, downloader RouteDownloader) {
	owner.mu.Lock()
	if owner.closed || owner.singleDownloadActive != "" {
		owner.mu.Unlock()
		return
	}
	if len(owner.singleDownloads) == 0 {
		if owner.bandwidth == "single-download-queue" {
			owner.bandwidth = ""
		}
		owner.mu.Unlock()
		return
	}
	job := owner.singleDownloads[0]
	owner.singleDownloads[0] = singleDownload{}
	owner.singleDownloads = owner.singleDownloads[1:]
	owner.singleDownloadActive = job.id
	owner.probes.Add(1)
	owner.mu.Unlock()
	go c.runDownload(job.ctx, job.cancel, owner, downloader, job.profile, job.revision, job.id)
}

// pruneSingleDownloads runs on the existing product owner, so cancelling a
// queued item never waits for the active download to complete.
func (c *Controller) pruneSingleDownloads() {
	c.mu.Lock()
	owner := c.tests
	downloader, available := c.tester.(RouteDownloader)
	c.mu.Unlock()
	if owner == nil {
		return
	}
	owner.mu.Lock()
	var removed []singleDownload
	kept := owner.singleDownloads[:0]
	for _, job := range owner.singleDownloads {
		record, exists := c.routes.catalog.Find(job.profile.ID)
		if job.ctx.Err() != nil || !exists || record.Hidden {
			removed = append(removed, job)
		} else {
			kept = append(kept, job)
		}
	}
	for i := len(kept); i < len(owner.singleDownloads); i++ {
		owner.singleDownloads[i] = singleDownload{}
	}
	owner.singleDownloads = kept
	for _, job := range removed {
		delete(owner.ops, job.id)
	}
	owner.mu.Unlock()
	for _, job := range removed {
		job.cancel()
		if c.routes.downloading[job.profile.ID] == job.id {
			delete(c.routes.downloading, job.profile.ID)
		}
		c.completeOperation(job.id, "下载测速", "operation_cancelled")
	}
	if len(removed) > 0 && available {
		c.startSingleDownload(owner, downloader)
	}
}
