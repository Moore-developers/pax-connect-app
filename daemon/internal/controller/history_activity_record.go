package controller

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

func (r *historyRead) materializeRecord(record *pb.LogRecord) *pb.LogRecord {
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
