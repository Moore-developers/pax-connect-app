package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/stats"
	"pax-connect/daemon/internal/storage"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type routeData struct {
	history                  config.RouteHistory
	historyStore             *config.RouteHistoryStore
	usage                    stats.Document
	usageFile                *storage.PrivateFile
	historyError, usageError string
	sampleError              string
	tags                     map[string]string
	live                     config.UsageSnapshot
	// revision mirrors history.Revision for the history publisher, which runs outside the owner.
	revision atomic.Uint64
}

// ConfigureRouteData owns product persistence at the existing operation queue.
func (c *Controller) ConfigureRouteData(directory string, owner uint32) {
	d := &routeData{history: config.NewRouteHistory(), historyStore: config.NewRouteHistoryStore(directory+"/route-history.json", owner), usage: stats.New(), usageFile: storage.NewPrivateFile(directory+"/route-usage.json", owner, 16<<20), tags: map[string]string{"unattributed": "unattributed"}}
	if v, err := d.historyStore.Load(); err == nil {
		d.history = v
	} else {
		d.historyError = "history_store_failed"
	}
	d.revision.Store(d.history.Revision)
	// route-usage.json was imported by the retired history migration. Current
	// history is SQLite; never inspect that obsolete file during route assembly.

	install := func(context.Context) {
		c.mu.Lock()
		c.data = d
		c.mu.Unlock()
		c.publishUsage()
	}
	if c.proxy != nil {
		_ = c.query(context.Background(), install)
	} else {
		install(context.Background())
	}
}
func (c *Controller) appendHistory(ctx context.Context, v config.HistorySample) {
	if c.data == nil || c.data.historyError != "" {
		return
	}
	c.data.history.Append(v, time.Now())
	c.persistHistory(ctx)
}

// pruneHistory applies retention before a read. A trim moves the revision, so it is saved and
// announced like a new point: a client holding the old revision must refetch.
func (c *Controller) pruneHistory(ctx context.Context) {
	before := c.data.history.Revision
	c.data.history.Prune(time.Now())
	if c.data.history.Revision != before {
		c.persistHistory(ctx)
	}
}

// persistHistory saves the owner's history, mirrors its revision and wakes the history publisher
// (R4: the revision reaches readers only together with the file it describes).
func (c *Controller) persistHistory(ctx context.Context) {
	d := c.data
	if err := d.historyStore.Save(ctx, d.history); err != nil {
		d.historyError = "history_store_failed"
		c.routes.measurementError = "history_store_failed"
	}
	d.revision.Store(d.history.Revision)
	select {
	case c.logsChanged <- struct{}{}:
	default:
	}
}

// routeHistoryRevision is the published route_history_revision; zero before route data exists.
func (c *Controller) routeHistoryRevision() uint64 {
	c.mu.Lock()
	d := c.data
	c.mu.Unlock()
	if d == nil {
		return 0
	}
	return d.revision.Load()
}
func (c *Controller) sampleUsage(ctx context.Context, final bool) {
	if c.history != nil {
		return
	}
	if c.data == nil {
		return
	}
	d := c.data
	fresh := false
	if core, ok := c.coreSession.(interface {
		ReadUsage(context.Context) (config.UsageSnapshot, error)
	}); ok {
		sample, err := core.ReadUsage(ctx)
		if err == nil {
			fresh = true
			d.sampleError = ""
			d.live = sample
			d.usage.Accept(sample, d.tags, time.Now())
			c.publishUsage()
		}
	}
	c.mu.Lock()
	idleWithoutCore := c.current.Status == pb.ProtectionStatus_PROTECTION_STATUS_OFF && d.live.Session == ""
	c.mu.Unlock()
	if !fresh && idleWithoutCore {
		// Before the first ON there is no root traffic stream to lose.
		if final {
			c.checkpointUsage(ctx, true)
		}
		return
	}
	if !fresh {
		d.usage.Partial = true
		d.sampleError = "usage_sample_failed"
		d.live.ConnectionsKnown = false
	}
	if final {
		c.checkpointUsage(ctx, fresh)
	}

}
func (c *Controller) checkpointUsage(ctx context.Context, final bool) {
	if c.history != nil {
		return
	}
	if c.data == nil {
		return
	}
	d := c.data
	if d.usageError != "" {
		return
	}
	d.usage.Clean = final
	raw, err := json.Marshal(d.usage)
	if err == nil {
		err = d.usageFile.Write(ctx, raw)
	}
	if err != nil {
		d.usageError = "usage_store_failed"
	}
}
func dataCursor(req *pb.RouteHistoryRequest, revision uint64, offset int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s/%s/%v/%v/%d", req.RouteId, req.Metric, req.From, req.Until, revision)))
	return hex.EncodeToString(sum[:12]) + ":" + strconv.Itoa(offset)
}
func pageOffset(req *pb.RouteHistoryRequest, revision uint64) (int, error) {
	if req.Cursor == "" {
		return 0, nil
	}
	parts := strings.Split(req.Cursor, ":")
	if len(parts) != 2 {
		return 0, errors.New("history_cursor_stale")
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil || n < 0 || n > 100000 || dataCursor(req, revision, n) != req.Cursor {
		return 0, errors.New("history_cursor_stale")
	}
	return n, nil
}
func inWindow(at time.Time, req *pb.RouteHistoryRequest) bool {
	return (req.From == nil || !at.Before(req.From.AsTime())) && (req.Until == nil || !at.After(req.Until.AsTime()))
}
func (c *Controller) QueryRouteHistory(ctx context.Context, req *pb.RouteHistoryRequest) (*pb.RouteHistoryResponse, error) {
	out := &pb.RouteHistoryResponse{}
	var failure error
	err := c.query(ctx, func(ctx context.Context) {
		if c.data == nil {
			failure = errors.New("history_unavailable")
			return
		}
		if c.data.historyError != "" {
			failure = errors.New(c.data.historyError)
			return
		}

		c.pruneHistory(ctx)
		offset, e := pageOffset(req, c.data.history.Revision)
		if e != nil {
			failure = e
			return
		}
		limit := min(100, max(1, int(req.Limit)))
		if req.Limit == 0 {
			limit = config.HistoryLimit(req.Metric)
		}
		items := []config.HistorySample{}
		for _, v := range c.data.history.Items {
			if v.RouteID == req.RouteId && (req.Metric == "" || v.Metric == req.Metric) && inWindow(v.At, req) {
				items = append(items, v)
			}
		}
		if offset > len(items) {
			failure = errors.New("history_cursor_stale")
			return
		}
		end := min(len(items), offset+limit)
		for _, v := range items[offset:end] {
			item := &pb.RouteHistorySample{Metric: v.Metric, At: timestamppb.New(v.At), ConfigRevision: v.ConfigRevision, Value: v.Value, Success: v.Success, ExitIp: v.ExitIP, Network: v.Network, Method: v.Method, ExitLocation: exitLocationProto(v.ExitLocation)}
			if v.Error != "" {
				item.Error = &pb.ProductError{Code: v.Error}
			}
			out.Items = append(out.Items, item)
		}
		if end < len(items) {
			out.NextCursor = dataCursor(req, c.data.history.Revision, end)
		}
	})
	if err != nil {
		return nil, err
	}
	if failure != nil {
		return nil, status.Error(codes.FailedPrecondition, failure.Error())
	}
	return out, nil
}
func (c *Controller) QueryRouteUsage(ctx context.Context, req *pb.RouteHistoryRequest) (*pb.RouteUsageResponse, error) {
	if c.history != nil {
		return c.historyRouteUsage(ctx, req)
	}
	out := &pb.RouteUsageResponse{}
	var failure error
	err := c.query(ctx, func(context.Context) {
		if c.data == nil {
			failure = errors.New("usage_unavailable")
			return
		}
		d := c.data

		if d.usageError != "" {
			out.Error = &pb.ProductError{Code: d.usageError}
			out.Partial = true
			return
		}
		offset, e := pageOffset(req, d.usage.Revision)
		if e != nil {
			failure = e
			return
		}
		limit := min(100, max(1, int(req.Limit)))
		if req.Limit == 0 {
			limit = 100
		}
		items := []stats.Bucket{}
		for _, b := range d.usage.Buckets {
			if b.RouteID == req.RouteId && b.At.After(time.Now().Add(-7*24*time.Hour)) && inWindow(b.At, req) {
				items = append(items, b)
			}
		}
		sort.Slice(items, func(i, j int) bool { return items[i].At.After(items[j].At) })
		if offset > len(items) {
			failure = errors.New("history_cursor_stale")
			return
		}
		end := min(len(items), offset+limit)
		for _, v := range items[offset:end] {
			out.Items = append(out.Items, &pb.RouteUsageBucket{At: timestamppb.New(v.At), Upload: v.Upload, Download: v.Download, Gap: v.Gap})
		}
		if end < len(items) {
			out.NextCursor = dataCursor(req, d.usage.Revision, end)
		}
		out.TodayUpload, out.TodayDownload, out.Partial = d.usage.Today(req.RouteId, time.Now())
		out.Partial = out.Partial || len(items) == 0
		c.mu.Lock()
		current := c.current.GetCurrentRoute().GetId()
		c.mu.Unlock()
		if d.sampleError != "" {
			out.Error = &pb.ProductError{Code: d.sampleError}
			out.Partial = true
		}
		if current == req.RouteId && d.live.ConnectionsKnown {
			out.SampledAt = timestamppb.New(d.live.At)
			for _, v := range d.live.Counters {
				if d.tags[v.Tag] == req.RouteId {
					out.Connections += v.Connections
				}
			}
		}
	})
	if err != nil {
		return nil, err
	}
	if failure != nil {
		return nil, status.Error(codes.FailedPrecondition, failure.Error())
	}
	return out, nil
}

// publishUsage projects the same persisted owner into the existing Dashboard summary.
func (c *Controller) publishUsage() {
	d := c.data
	totals := &pb.TrafficSnapshot{}
	seen := map[string]bool{}
	for _, day := range d.usage.Days {
		seen[day.RouteID] = true
	}
	for id := range seen {
		up, down, _ := d.usage.Today(id, time.Now())
		totals.UploadBytes += up
		totals.DownloadBytes += down
		totals.UnclassifiedBytes += up + down // Native aggregate has no per-day rule category.
	}
	if d.live.ConnectionsKnown && time.Since(d.live.At) >= 0 && time.Since(d.live.At) <= 3*time.Second {
		for _, v := range d.live.Counters {
			totals.ActiveConnections += v.Connections
		}
	}
	c.mu.Lock()
	c.current.TodayTraffic = totals
	c.commitLocked()
	c.mu.Unlock()
}

func exitLocationProto(v *config.ExitLocation) *pb.ExitLocation {
	if v == nil {
		return nil
	}
	out := &pb.ExitLocation{Ip: v.IP, CountryCode: v.CountryCode, Country: v.Country, Region: v.Region, City: v.City, Isp: v.ISP, Asn: v.ASN, Source: v.Source, GeoSource: v.GeoSource, GeoError: v.GeoError, Network: v.Network, ConfigRevision: v.ConfigRevision, At: timestamppb.New(v.At)}
	if !v.GeoAt.IsZero() {
		out.GeoAt = timestamppb.New(v.GeoAt)
	}
	return out
}
