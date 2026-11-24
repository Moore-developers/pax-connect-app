package controller

import (
	"context"
	"math"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

func alertSettingsProto(s config.TrafficAlertSettings) *pb.TrafficAlertSettings {
	rule := func(r config.TrafficAlertRule) *pb.TrafficAlertRule {
		return &pb.TrafficAlertRule{Enabled: r.Enabled, Upload: r.Upload, Download: r.Download}
	}
	return &pb.TrafficAlertSettings{Revision: s.Revision, Enabled: s.Enabled, MediumSeconds: s.MediumSeconds, Short: rule(s.Short), Hour: rule(s.Hour), TwoHours: rule(s.TwoHours), Day: rule(s.Day)}
}
func alertSettingsValue(s *pb.TrafficAlertSettings) config.TrafficAlertSettings {
	rule := func(r *pb.TrafficAlertRule) config.TrafficAlertRule {
		return config.TrafficAlertRule{Enabled: r.GetEnabled(), Upload: r.GetUpload(), Download: r.GetDownload()}
	}
	return config.TrafficAlertSettings{Revision: s.Revision, Enabled: s.Enabled, MediumSeconds: s.MediumSeconds, Short: rule(s.Short), Hour: rule(s.Hour), TwoHours: rule(s.TwoHours), Day: rule(s.Day)}
}
func alertNoticeProto(n config.TrafficAlertNotice) *pb.TrafficAlertNotice {
	return &pb.TrafficAlertNotice{Id: n.ID, Key: n.Key, Source: n.Source, SourceKind: n.SourceKind, Direction: n.Direction, WindowSeconds: n.WindowSeconds, Bytes: n.Bytes, Threshold: n.Threshold, Until: timestamppb.New(n.Until), RouteId: n.RouteID, RouteName: n.RouteName, RouteCount: n.RouteCount, Partial: n.Partial}
}

func (c *Controller) GetTrafficAlerts(context.Context, *emptypb.Empty) (*pb.TrafficAlertState, error) {
	if p := c.trafficAlerts; p != nil {
		select {
		case p.wake <- struct{}{}:
		default:
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current.TrafficAlerts == nil {
		return nil, status.Error(codes.FailedPrecondition, "traffic_alerts_unavailable")
	}
	return proto.Clone(c.current.TrafficAlerts).(*pb.TrafficAlertState), nil
}

func (c *Controller) SaveTrafficAlertSettings(ctx context.Context, q *pb.TrafficAlertSettings) (*pb.TrafficAlertState, error) {
	if q == nil || alertSettingsValue(q).Validate() != nil {
		return nil, status.Error(codes.InvalidArgument, "traffic_alerts_invalid")
	}
	var result *pb.TrafficAlertState
	evidence := &config.SystemEvent{Type: "settings.traffic_alerts", Name: "保存流量提醒", Category: config.EventCategory{Key: "settings", Name: "设置与数据", Symbol: "bell"}, Result: "completed"}
	err := c.routingCommandEvidence(ctx, evidence, func(operation context.Context) error {
		p := c.trafficAlerts
		if p == nil {
			return status.Error(codes.FailedPrecondition, "traffic_alerts_unavailable")
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.failure != nil {
			return status.Error(codes.FailedPrecondition, "traffic_alerts_storage_failed")
		}
		if q.Revision != p.settings.Revision {
			return status.Error(codes.Aborted, errTrafficAlertConflict.Error())
		}
		if alertSettingsValue(q) == p.settings {
			c.mu.Lock()
			result = proto.Clone(c.current.TrafficAlerts).(*pb.TrafficAlertState)
			c.mu.Unlock()
			return nil
		}
		if p.settings.Revision == math.MaxUint64 {
			return status.Error(codes.FailedPrecondition, "traffic_alerts_invalid")
		}
		next := alertSettingsValue(q)
		next.Revision++
		if err := p.store.SaveSettings(operation, next); err != nil {
			loaded, loadErr := p.store.LoadSettings()
			if loadErr != nil || loaded != p.settings {
				p.failure = err
			}
			return status.Error(codes.Internal, "traffic_alerts_save_failed")
		}
		p.settings = next
		p.runtime = config.TrafficAlertRuntime{Revision: next.Revision, Episodes: map[string]config.TrafficAlertEpisode{}}
		result = &pb.TrafficAlertState{Settings: alertSettingsProto(next), MonitorState: "checking"}
		if !next.Enabled {
			result.MonitorState = "disabled"
		}
		c.publishTrafficAlerts(result)
		select {
		case p.wake <- struct{}{}:
		default:
		}
		return nil
	})
	return result, err
}
