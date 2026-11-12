package controller

import (
	"context"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
)

type frozenRouteTest struct {
	record config.RouteRecord
	view   config.TLSPolicyView
	id     string
}

// testBatch admits a bounded, frozen scope in one owner transaction. The RPC
// only waits for admission; client disconnection never stops the batch.
func (c *Controller) testBatch(ctx context.Context, req *paxv1.TestRouteRequest) (*paxv1.OperationAck, error) {
	download := req.Kind == "download"
	limit := 1000 // Catalog bound; twenty is a download chunk, not a scope limit.
	if len(req.Targets) > limit || req.RouteId != "" || (req.Kind != "" && req.Kind != "latency" && !download) {
		return nil, status.Error(codes.InvalidArgument, "test_batch_invalid")
	}
	if c.tests == nil || c.tester == nil || c.routes == nil || c.proxy == nil {
		return nil, status.Error(codes.FailedPrecondition, "test_unavailable")
	}
	if _, ok := c.tester.(RouteDownloader); download && !ok {
		return nil, status.Error(codes.FailedPrecondition, "download_unavailable")
	}
	id, err := operationID()
	if err != nil {
		return nil, status.Error(codes.Internal, "operation id unavailable")
	}
	owner := c.tests
	var failure error
	err = c.query(ctx, func(ctx context.Context) {
		items := make([]frozenRouteTest, 0, len(req.Targets))
		seen := map[string]bool{}
		for _, target := range req.Targets {
			record, ok := c.routes.catalog.Find(target.GetRouteId())
			if !ok || record.Hidden || seen[target.GetRouteId()] {
				failure = status.Error(codes.InvalidArgument, "test_batch_invalid")
				return
			}
			seen[target.RouteId] = true
			if record.Revision != target.ExpectedRouteRevision {
				failure = status.Error(codes.FailedPrecondition, "route_stale")
				return
			}
			if c.routes.testing[target.RouteId] != "" || c.routes.downloading[target.RouteId] != "" {
				failure = status.Error(codes.FailedPrecondition, "operation_busy")
				return
			}
			resolved, resolveErr := c.resolvedRecord(ctx, record)
			if resolveErr != nil {
				failure = resolveErr
				return
			}
			record = resolved
			view := record.Config.TLSResolved
			if record.Chain == nil && c.tls != nil && record.Config.Security == "tls" {
				var resolveErr error
				view, resolveErr = c.tls.Resolve(ctx, record.Config)
				if resolveErr != nil {
					failure = status.Error(codes.FailedPrecondition, tlsErrorCode(resolveErr))
					return
				}
			}
			if view.NeedsConfirmation {
				failure = status.Error(codes.FailedPrecondition, "tls_confirmation_required")
				return
			}
			if target.ExpectedConfigRevision != 0 && connectionVersion(record, view) != target.ExpectedConfigRevision {
				failure = status.Error(codes.FailedPrecondition, "route_stale")
				return
			}
			child, childErr := operationID()
			if childErr != nil {
				failure = status.Error(codes.Internal, "operation id unavailable")
				return
			}
			items = append(items, frozenRouteTest{record: record, view: view, id: child})
		}
		owner.mu.Lock()
		if owner.closed || owner.batch != "" || (download && owner.bandwidth != "") {
			owner.mu.Unlock()
			failure = status.Error(codes.ResourceExhausted, "operation_busy")
			return
		}
		task, cancel := context.WithCancel(owner.lifetime)
		owner.batch = id
		owner.batchDownload = download
		if download {
			owner.bandwidth = id
		}
		owner.ops[id] = cancel
		if !download {
			for _, item := range items {
				owner.parents[item.id] = id
			}
		}
		owner.probes.Add(1 + len(items))
		owner.mu.Unlock()
		c.measurementBatchHolds++
		for _, item := range items {
			if download {
				c.routes.downloading[item.record.Config.ID] = id
			} else {
				c.routes.testing[item.record.Config.ID] = item.id
			}
		}
		c.publishCatalog(ctx)
		parallel, _, downloadParallel := c.measurementParallel()
		_, exitParallel, _ := c.measurementParallel()
		for i := range items {
			items[i].record.Config.MeasurementLatency = parallel
			items[i].record.Config.MeasurementExit = exitParallel
			items[i].record.Config.MeasurementParallel = downloadParallel
		}
		if download {
			parallel = downloadParallel
		}
		go c.runTestBatch(task, cancel, owner, items, id, download, parallel)
	})
	if err != nil {
		return nil, err
	}
	if failure != nil {
		return nil, failure
	}
	return &paxv1.OperationAck{OperationId: id}, nil
}

func (c *Controller) runTestBatch(ctx context.Context, cancel context.CancelFunc, owner *testOwner, items []frozenRouteTest, id string, download bool, parallel int) {
	defer owner.probes.Done()
	defer cancel()
	defer func() {
		owner.mu.Lock()
		delete(owner.ops, id)
		owner.batch = ""
		if owner.bandwidth == id {
			owner.bandwidth = ""
		}
		owner.mu.Unlock()
	}()
	var failures int32
	chunkSize := len(items)
	if download {
		chunkSize = measurement.MaxDownloadBatch
	}
	for start := 0; start < len(items); start += chunkSize {
		chunk := items[start:min(start+chunkSize, len(items))]
		failures += c.runTestChunk(ctx, owner, chunk, download, parallel)
		// Per-route results were enqueued before this owner barrier. Persist each
		// completed chunk before any work in the next chunk starts.
		if err := c.query(owner.lifetime, func(queryCtx context.Context) {
			c.measurementBatchHolds--
			c.persistMeasurements(queryCtx)
			c.measurementBatchHolds++
			c.publishCatalog(queryCtx)
		}); err != nil {
			cancel() // Drain remaining children without I/O so shutdown cannot strand probes.
		}
	}
	cancelled := ctx.Err() != nil
	if !c.enqueueCatalogOp(id, "整组测试", func(context.Context) error {
		c.measurementBatchHolds--
		owner.mu.Lock()
		if owner.batch == id {
			owner.batch = ""
		}
		owner.mu.Unlock()
		// The final chunk has already been checkpointed by the owner barrier.
		c.publishCatalog(c.proxy.lifetime)
		if cancelled {
			return &productError{code: "operation_cancelled"}
		}
		if failures > 0 {
			return &productError{code: "test_batch_partial"}
		}
		return nil
	}) {
		c.completeOperation(id, "整组测试", "operation_cancelled")
	}
}

func (c *Controller) runTestChunk(ctx context.Context, owner *testOwner, items []frozenRouteTest, download bool, parallel int) int32 {
	jobs := make(chan frozenRouteTest, len(items))
	for _, item := range items {
		jobs <- item
	}
	close(jobs)
	if !download {
		parallel = measurement.HardParallel
	}
	workers := min(parallel, len(items))
	var finished sync.WaitGroup
	var failures atomic.Int32
	for i := 0; i < workers; i++ {
		finished.Add(1)
		go func() {
			defer finished.Done()
			for item := range jobs {
				var code string
				task, stop := context.WithCancel(ctx)
				if download {
					profile := item.record.Config
					profile.TLSResolved = item.view
					code = c.runDownload(task, stop, owner, c.tester.(RouteDownloader), profile, connectionVersion(item.record, item.view), item.id)
				} else {
					code = c.runRouteTest(task, stop, item.record, item.view, item.id, owner, c.tester)
				}
				if code != "" {
					failures.Add(1)
				}
			}
		}()
	}
	finished.Wait()
	return failures.Load()
}
