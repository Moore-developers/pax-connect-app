package controller

import (
	"context"
	"sort"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

// A group shares its projection and event identities until one member or its summary changes.
// Query-dependent duration, collection state and installed-application classification stay local.
type logActivityGroup struct {
	events []config.HistoryEvent
	ids    []string
	record *pb.LogRecord
	until  time.Time
	bytes  int
}

func buildActivityGroup(ctx context.Context, v *logModelVersion, events []config.HistoryEvent) (*logActivityGroup, error) {
	sort.Slice(events, func(i, j int) bool {
		if events[i].At.Equal(events[j].At) {
			return events[i].ID > events[j].ID
		}
		return events[i].At.After(events[j].At)
	})
	until := events[0].At
	if f := events[0].Flow; f != nil {
		if s := v.connections[f.Key]; s != nil && s.summary.Metrics.AsOf.After(until) {
			until = s.summary.Metrics.AsOf
		}
	}
	r := &historyRead{logs: events, logVersion: v, until: until, meta: historyMetadata{Collection: "collecting"}}
	if err := freezeLogRecordsUncached(ctx, nil, r); err != nil {
		return nil, err
	}
	g := &logActivityGroup{events: events, until: until}
	if len(r.records) > 0 {
		g.record = r.records[0]
		g.ids = r.recordEvents[g.record.RecordId]
		g.bytes = proto.Size(g.record) + len(g.ids)*40 + len(g.events)*256 + 256
	}
	return g, nil
}

func (v *logModelVersion) buildActivity(ctx context.Context, old *logModelVersion, changed map[string]bool, dEvents []config.HistoryEvent) error {
	v.activity = map[string]*logActivityGroup{}
	if old != nil {
		for key, g := range old.activity {
			v.activity[key] = g
		}
	}
	groups := map[string][]config.HistoryEvent{}
	for key := range changed {
		if old != nil && old.activity[key] != nil {
			groups[key] = append([]config.HistoryEvent(nil), old.activity[key].events...)
		}
	}
	for _, event := range dEvents {
		key := logViewKey(event, "activity")
		groups[key] = append(groups[key], event)
	}
	for key, events := range groups {
		if len(events) == 0 {
			continue
		}
		g, err := buildActivityGroup(ctx, v, events)
		if err != nil {
			return err
		}
		v.activity[key] = g
	}
	for _, g := range v.activity {
		v.bytes += g.bytes
	}
	return nil
}

func (c *Controller) freezeLogModelRecords(ctx context.Context, source logReads, r *historyRead) error {
	selected := map[string][]config.HistoryEvent{}
	for _, event := range r.logs {
		key := logViewKey(event, "activity")
		selected[key] = append(selected[key], event)
	}
	r.records = make([]*pb.LogRecord, 0, len(selected))
	r.recordByID = map[string]*pb.LogRecord{}
	r.recordEvents = map[string][]string{}
	r.bytes = 0
	for key, events := range selected {
		if err := ctx.Err(); err != nil {
			return err
		}
		g := r.logVersion.activity[key]
		// A time or route boundary may cut through a group. Reuse the exact legacy projection
		// for that subset; complete groups use their shared immutable record.
		if g == nil || len(g.events) != len(events) || g.until.After(r.until) {
			local := &historyRead{logs: events, logVersion: r.logVersion, until: r.until, meta: r.meta}
			if err := freezeLogRecordsUncached(ctx, source, local); err != nil {
				return err
			}
			for _, record := range local.records {
				r.records = append(r.records, record)
				r.recordByID[record.RecordId] = record
				r.recordEvents[record.RecordId] = local.recordEvents[record.RecordId]
			}
			r.bytes += local.bytes
			continue
		}
		r.records = append(r.records, g.record)
		r.recordByID[key] = g.record
		r.recordEvents[key] = g.ids
		r.bytes += len(key) + 192
	}
	sort.Slice(r.records, func(i, j int) bool {
		a, b := r.records[i], r.records[j]
		if a.LastAt.AsTime().Equal(b.LastAt.AsTime()) {
			return a.RecordId > b.RecordId
		}
		return a.LastAt.AsTime().After(b.LastAt.AsTime())
	})
	r.logs = nil
	return nil
}

func (r *historyRead) materializeRecord(record *pb.LogRecord) *pb.LogRecord {
	if r.logVersion == nil {
		return record
	}
	out := proto.Clone(record).(*pb.LogRecord)
	out.AsOf = timestamppb.New(r.until)
	if out.Flow != nil {
		if app, ok := r.userApplications[out.RecordId]; ok {
			out.CategoryName, out.CategorySymbol = app.Name, "app"
		} else {
			_, out.CategoryName = logSystemSource(out)
			out.CategorySymbol = "gearshape"
		}
	}
	if out.Flow == nil {
		return out
	}
	f := out.Flow
	m := out.Metrics
	if m == nil {
		return out
	}
	fact := config.FlowFact{StartedAt: f.StartedAt.AsTime(), StateUnknown: f.StateUnknown}
	if f.ClosedAt != nil {
		t := f.ClosedAt.AsTime()
		fact.ClosedAt = &t
	}
	if f.ObservedUntil != nil {
		t := f.ObservedUntil.AsTime()
		fact.ObservedUntil = &t
	}
	metrics := config.ConnectionMetrics{Upload: m.Upload, Download: m.Download, BytesState: m.BytesState, Reason: m.Reason}
	if m.AsOf != nil {
		metrics.AsOf = m.AsOf.AsTime()
	}
	if m.BytesFrom != nil {
		metrics.BytesFrom = m.BytesFrom.AsTime()
	}
	if fact.ClosedAt == nil && r.meta.Collection != "collecting" && !metrics.AsOf.IsZero() {
		fact.StateUnknown = true
		fact.ObservedUntil = &metrics.AsOf
		f.StateUnknown = true
		f.ObservedUntil = timestamppb.New(metrics.AsOf)
	}
	metrics = deriveConnectionDuration(metrics, &fact, r.until)
	out.Metrics = metricsProto(metrics)
	return out
}

func classifySharedLogSources(records []*pb.LogRecord, apps []config.RoutingApplication) map[string]config.RoutingApplication {
	result := map[string]config.RoutingApplication{}
	for _, record := range records {
		if f := record.Flow; f != nil {
			if app, ok := userApplicationMatch(f.ApplicationId, f.Process, apps); ok {
				result[record.RecordId] = app
			}
		}
	}
	return result
}
