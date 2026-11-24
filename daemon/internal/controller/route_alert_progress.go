package controller

import (
	"context"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/historystore"
)

type routeAlertPeriod struct {
	from   time.Time
	sample *pb.RouteAlertWindow
}

// The readout uses the request's pinned history and matching live tail. It
// doesn't wait for the independent per-source notification evaluator.
func (c *Controller) routeAlertProgress(ctx context.Context, snap *trafficSnapshot, proof *coverageSpan, route string) *pb.RouteAlertProgress {
	p := c.trafficAlerts
	if p == nil {
		return nil
	}
	p.mu.Lock()
	settings, failure := p.settings, p.failure
	p.mu.Unlock()
	c.mu.Lock()
	off := c.current.Status == pb.ProtectionStatus_PROTECTION_STATUS_OFF
	c.mu.Unlock()
	out := &pb.RouteAlertProgress{Settings: alertSettingsProto(settings)}
	switch {
	case failure != nil || snap.meta.Error != "":
		out.MonitorState = "failed"
	case !settings.Enabled:
		out.MonitorState = "disabled"
	case off:
		out.MonitorState = "proxy_off"
	case proof == nil || !proof.collecting || snap.now.Sub(proof.last) > trafficPollSpacing:
		out.MonitorState = "collection_interrupted"
	default:
		out.MonitorState = "collecting"
	}
	if out.MonitorState != "collecting" {
		return out
	}

	until := minTime(snap.now, proof.last).Truncate(trafficBucket5s)
	var periods []routeAlertPeriod
	boundary := until.Truncate(trafficBucket5m)
	recentFrom, archiveFrom := boundary, boundary
	for _, w := range enabledAlertWindows(settings) {
		from := until.Add(-time.Duration(w.seconds) * time.Second)
		if w.seconds == 300 {
			recentFrom = from
		} else {
			// Older history has five-minute precision. Use only buckets inside
			// the window, then add the recent tail through the common cutoff.
			from = firstFullTrafficBucket(from)
			archiveFrom = minTime(archiveFrom, from)
		}
		periods = append(periods, routeAlertPeriod{from: from, sample: &pb.RouteAlertWindow{
			Seconds: w.seconds, Until: timestamppb.New(until),
		}})
	}
	if len(periods) == 0 {
		return out
	}
	if err := readRouteAlertPeriods(ctx, snap, proof, route, periods, archiveFrom, recentFrom, until); err != nil {
		out.MonitorState = "failed"
		return out
	}
	for _, period := range periods {
		if period.sample.Partial {
			out.MonitorState = "partial"
		}
		out.Windows = append(out.Windows, period.sample)
	}
	return out
}

func readRouteAlertPeriods(ctx context.Context, snap *trafficSnapshot, proof *coverageSpan, route string, periods []routeAlertPeriod, archiveFrom, recentFrom, until time.Time) error {
	earliest := minTime(archiveFrom, recentFrom)
	coverage, err := snap.coverageWithPoll(ctx, earliest, until, proof)
	if err != nil {
		return err
	}
	for _, period := range periods {
		period.sample.Partial = snap.compact.Partial ||
			!fullAggregateCoverage(coverage, period.from, until)
	}
	boundary := until.Truncate(trafficBucket5m)
	add := func(at time.Time, up, down uint64, archived, legacy bool) {
		for _, period := range periods {
			if at.Before(period.from) || (archived && period.sample.Seconds == 300) ||
				(!archived && at.Before(boundary) && period.sample.Seconds != 300) {
				continue
			}
			if legacy {
				period.sample.Partial = true
			} else {
				period.sample.Upload += up
				period.sample.Download += down
			}
		}
	}
	dimensions := []string{"route"}
	read := func(recent bool, from, end time.Time) error {
		if !from.Before(end) {
			return nil
		}
		return snap.read.Rows(ctx, recent, from, end, dimensions, func(row historystore.TrafficRow) error {
			if row.Object == route {
				add(time.UnixMilli(row.BucketMs), uint64(row.UpBytes), uint64(row.DownBytes), !recent, row.LegacyGap)
			}
			return nil
		})
	}
	if err = read(false, archiveFrom, boundary); err != nil {
		return err
	}
	if err = read(true, recentFrom, until); err != nil {
		return err
	}
	// The uncommitted tail has a single five-second representation. Older
	// uncommitted rows can fill both the short cut and its long-period bucket.
	for _, row := range pendingBuckets(snap.entries, earliest, until) {
		if row.Identity.Dimension != "route" || row.Identity.Object != route {
			continue
		}
		for _, period := range periods {
			if !row.At.Before(period.from) {
				period.sample.Upload += row.Upload
				period.sample.Download += row.Download
			}
		}
	}
	return nil
}
