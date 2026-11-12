package controller

import (
	"context"
	"errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
	"sync"
	"sync/atomic"
	"time"
)

// RouteExitLookup is an independent no-TUN exit IP/region query.
type RouteExitLookup interface {
	LookupRouteExit(context.Context, config.RouteConfig) (config.RouteMeasurement, error)
}

type exitQuery struct {
	profile  config.RouteConfig
	revision uint64
}

const exitLabel = "查询 IP 地区"

func (c *Controller) testExit(ctx context.Context, req *pb.TestRouteRequest) (*pb.OperationAck, error) {
	lookup, ok := c.tester.(RouteExitLookup)
	if !ok || c.tests == nil || c.routes == nil || c.proxy == nil {
		return nil, status.Error(codes.FailedPrecondition, "exit_unavailable")
	}
	targets := req.Targets
	if req.RouteId != "" {
		if len(targets) > 0 {
			return nil, status.Error(codes.InvalidArgument, "test_batch_invalid")
		}
		targets = []*pb.RouteTestTarget{{RouteId: req.RouteId, ExpectedRouteRevision: req.ExpectedRouteRevision}}
	}
	if len(targets) == 0 || len(targets) > 1000 {
		return nil, status.Error(codes.InvalidArgument, "test_batch_invalid")
	}
	var ack *pb.OperationAck
	var failure error
	err := c.query(ctx, func(ctx context.Context) {
		owner := c.tests
		if len(targets) == 1 && targets[0] != nil {
			if id := c.routes.exiting[targets[0].RouteId]; id != "" {
				ack = &pb.OperationAck{OperationId: id}
				return
			}
		}
		seen := map[string]bool{}
		items := make([]exitQuery, 0, len(targets))
		for _, target := range targets {
			record, exists := c.routes.catalog.Find(target.GetRouteId())
			if !exists || record.Hidden || seen[target.GetRouteId()] {
				failure = status.Error(codes.InvalidArgument, "test_batch_invalid")
				return
			}
			seen[target.RouteId] = true
			if record.Revision != target.ExpectedRouteRevision {
				failure = status.Error(codes.FailedPrecondition, "route_stale")
				return
			}
			if c.routes.exiting[target.RouteId] != "" {
				failure = status.Error(codes.FailedPrecondition, "operation_busy")
				return
			}
			resolved, e := c.resolvedRecord(ctx, record)
			if e != nil {
				failure = e
				return
			}
			p := resolved.Config
			if p.TLSResolved.NeedsConfirmation {
				failure = status.Error(codes.FailedPrecondition, "tls_confirmation_required")
				return
			}
			revision := connectionVersion(resolved, p.TLSResolved)
			if target.ExpectedConfigRevision != 0 && target.ExpectedConfigRevision != revision {
				failure = status.Error(codes.FailedPrecondition, "route_stale")
				return
			}
			items = append(items, exitQuery{p, revision})
		}
		id, e := operationID()
		if e != nil {
			failure = status.Error(codes.Internal, "operation id unavailable")
			return
		}
		owner.mu.Lock()
		if owner.closed || len(items) > 1 && owner.exitBatch != "" {
			owner.mu.Unlock()
			failure = status.Error(codes.ResourceExhausted, "operation_busy")
			return
		}
		task, cancel := context.WithCancel(owner.lifetime)
		owner.ops[id] = cancel
		if len(items) > 1 {
			owner.exitBatch = id
		}
		owner.probes.Add(1)
		owner.mu.Unlock()
		for _, item := range items {
			c.routes.exiting[item.profile.ID] = id
		}
		c.publishCatalog(ctx)
		parallel := measurement.DefaultParallel
		go c.runExitQueries(task, cancel, owner, lookup, items, id, parallel)
		ack = &pb.OperationAck{OperationId: id}
	})
	if err != nil {
		return nil, err
	}
	return ack, failure
}

func (c *Controller) runExitQueries(ctx context.Context, cancel context.CancelFunc, owner *testOwner, lookup RouteExitLookup, items []exitQuery, id string, parallel int) {
	defer owner.probes.Done()
	defer cancel()
	defer func() {
		owner.mu.Lock()
		delete(owner.ops, id)
		if owner.exitBatch == id {
			owner.exitBatch = ""
		}
		owner.mu.Unlock()
	}()
	jobs := make(chan exitQuery, len(items))
	for _, item := range items {
		jobs <- item
	}
	close(jobs)
	var wg sync.WaitGroup
	var failed atomic.Int32
	parallel = max(1, min(parallel, len(items)))
	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				if c.runExitQuery(ctx, owner, lookup, item, id) != "" {
					failed.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	code := ""
	if ctx.Err() != nil {
		code = "operation_cancelled"
	} else if failed.Load() > 0 {
		code = "exit_query_failed"
	}
	if !c.enqueueCatalogOp(id, exitLabel, func(ctx context.Context) error {
		c.persistMeasurements(ctx)
		c.publishCatalog(ctx)
		if code != "" {
			return &productError{code: code}
		}
		return nil
	}) {
		c.completeOperation(id, exitLabel, "operation_cancelled")
	}
}

func (c *Controller) runExitQuery(ctx context.Context, owner *testOwner, lookup RouteExitLookup, item exitQuery, id string) string {
	select {
	case <-owner.slots:
		defer func() { owner.slots <- struct{}{} }()
	case <-ctx.Done():
	}
	bounded, done := context.WithTimeout(ctx, measurement.ExitQueueTimeout+measurement.ExitTimeout+time.Second)
	defer done()
	profile := item.profile
	networkBefore := ""
	code := ""
	eligible := make(chan bool, 1)
	if err := c.query(bounded, func(ctx context.Context) {
		record, exists := c.routes.catalog.Find(item.profile.ID)
		if !exists || record.Hidden {
			eligible <- false
			return
		}
		resolved, err := c.resolvedRecord(ctx, record)
		eligible <- err == nil && connectionVersion(resolved, resolved.Config.TLSResolved) == item.revision
	}); err != nil {
		code = "operation_cancelled"
	} else {
		select {
		case ok := <-eligible:
			if !ok {
				code = "route_stale"
			}
		case <-bounded.Done():
			code = "operation_cancelled"
		}
	}
	if code == "" && c.physical != nil {
		physical, e := c.physical.sample(bounded)
		if e != nil {
			code = "physical_network_unavailable"
		} else {
			networkBefore = physical.Epoch
			profile.MeasurementNetwork = physical.Epoch
			profile = bindRouteNetwork(profile, physical)
		}
	}
	var value config.RouteMeasurement
	var params []string
	paramsCode := ""
	if code == "" && bounded.Err() == nil {
		var err error
		value, err = lookup.LookupRouteExit(bounded, profile)
		if err != nil {
			failure := testErrorCode(err, bounded)
			code = failure.Error()
			// Same params as the latency column: the typed failure's sanitized reason (HTTP / WS status).
			var product *productError
			if errors.As(failure, &product) {
				params, paramsCode = product.params, product.code
			}
		}
		if value.ExitError != "" {
			code = value.ExitError
		}
		if code == "" && value.ExitIP == "" {
			code = "exit_query_failed"
		}
	}
	if errors.Is(bounded.Err(), context.Canceled) {
		code = "operation_cancelled"
	} else if errors.Is(bounded.Err(), context.DeadlineExceeded) {
		code = "exit_timeout"
	}
	if networkBefore != "" && bounded.Err() == nil {
		latest, e := c.physical.sample(bounded)
		if e != nil || latest.Epoch != networkBefore {
			code = "network_changed"
		}
	}
	result := &pb.RouteExitSummary{At: timestamppb.Now(), Success: code == "", TestedConfigRevision: item.revision, ErrorCode: code}
	if code != "" && code == paramsCode && len(params) > 0 {
		result.ErrorParams = params
	}
	if code == "" {
		result.Ip = value.ExitIP
		if value.ExitLocation == nil {
			value.ExitLocation = &config.ExitLocation{IP: value.ExitIP, At: result.At.AsTime()}
		}
		location := *value.ExitLocation
		location.ConfigRevision = item.revision
		location.Network = networkBefore
		result.Location = exitLocationProto(&location)
		value.ExitLocation = &location
	}
	err := c.query(owner.lifetime, func(ctx context.Context) {
		if c.routes.exiting[profile.ID] != id {
			return
		}
		delete(c.routes.exiting, profile.ID)
		record, exists := c.routes.catalog.Find(profile.ID)
		if !exists || record.Hidden {
			code = "route_stale"
		}
		if exists && !record.Hidden {
			resolved, e := c.resolvedRecord(ctx, record)
			if e != nil || connectionVersion(resolved, resolved.Config.TLSResolved) != item.revision {
				code = "route_stale"
			} else if code != "operation_cancelled" && code != "network_changed" {
				if previous := c.routes.exitResults[profile.ID]; previous != nil && result.Location == nil {
					result.LastLocation = previous.Location
					if result.LastLocation == nil {
						result.LastLocation = previous.LastLocation
					}
				}
				if code == "ipv6_unavailable" {
					c.publishCatalog(ctx)
					return
				}
				c.routes.exitResults[profile.ID] = result
				c.appendHistory(ctx, config.HistorySample{RouteID: profile.ID, Metric: "exit", Network: networkBefore, At: result.At.AsTime(), ConfigRevision: item.revision, Success: result.Success, Error: result.ErrorCode, ExitIP: result.Ip, ExitLocation: value.ExitLocation})
			}
		}
		c.publishCatalog(ctx)
	})
	if err != nil {
		return "operation_cancelled"
	}
	return code
}
